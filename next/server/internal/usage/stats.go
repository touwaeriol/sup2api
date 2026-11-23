package usage

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // tz= accepts IANA names on minimal images

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/account"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Console statistics (CONTRACTS §66): the summary's day and hour buckets and
// the overview's "today" follow the caller's time zone, every statistics
// query runs under a statement timeout, and a timeout is an error the console
// shows as one, never as zeros.

// statsLimits are the settings of one statistics transaction. A range too
// large to answer in time is refused rather than left to hold a connection.
type statsLimits struct {
	// timeout is the statement timeout.
	timeout string
	// workMem, when set, is the transaction's work_mem. PostgreSQL guesses a
	// day or hour bucket has as many groups as there are rows and would sort
	// the wide rows on disk; with enough room it hashes the few real groups.
	workMem string
}

var (
	// directStats: a caller waits on the query alone (their own rows, the
	// realtime figures).
	directStats = statsLimits{timeout: "5s"}
	// snapshotStats: an operator's cached load (snapshot_cache.go) scans
	// everyone's rows, runs once per key however many callers wait, and must
	// end inside statsLoadTimeout.
	snapshotStats = statsLimits{timeout: "25s", workMem: "128MB"}
)

// statsLimitsFor gives the limits of an overview: an operator's or the
// account side's (self nil) is cached, the caller's own is not.
func statsLimitsFor(self *int64) statsLimits {
	if self == nil {
		return snapshotStats
	}
	return directStats
}

// maxHourSpan caps group_by=hour: 31 days is 744 buckets.
const maxHourSpan = 31 * 24 * time.Hour

// parseTZ reads tz=<IANA name> (default UTC). The same name goes to
// PostgreSQL's AT TIME ZONE, which core.LoadTimeZone keeps safe.
func parseTZ(c *gin.Context) (string, *time.Location, error) {
	name := c.DefaultQuery("tz", "UTC")
	loc, ok := core.LoadTimeZone(name)
	if !ok {
		return "", nil, core.ErrInvalidArgument.WithMessage("invalid tz (IANA time zone name)")
	}
	return name, loc, nil
}

// statsTx runs fn in a read-only transaction under lim.
func (s *Service) statsTx(ctx context.Context, lim statsLimits, fn func(*gorm.DB) error) error {
	return s.db.TxOptions(ctx, store.ReadOnly(), func(tx *gorm.DB) error {
		if err := store.SetLocal(ctx, tx, "statement_timeout", lim.timeout); err != nil {
			return err
		}
		if lim.workMem != "" {
			if err := store.SetLocal(ctx, tx, "work_mem", lim.workMem); err != nil {
				return err
			}
		}
		err := fn(tx)
		if store.SQLState(err) == "57014" { // query_canceled
			return core.ErrUnavailable.WithMessage("statistics query timed out; narrow the time range or filters")
		}
		return err
	})
}

// promptTokens matches expr.Normalize's inclusive/exclusive input
// convention, including both cache-write buckets. Missing historical
// semantics are not guessed from the client protocol (the gateway may have
// converted it): such a row with cached input gives NULL.
const promptTokens = `CASE
		WHEN u.cache_read_tokens + u.cache_creation_tokens + u.cache_creation_1h_tokens = 0 THEN u.input_tokens
		WHEN u.billing_detail->'inputs'->>'semantics' = 'inclusive' THEN u.input_tokens
		WHEN u.billing_detail->'inputs'->>'semantics' IN ('exclusive', '') THEN
			u.input_tokens + u.cache_read_tokens + u.cache_creation_tokens + u.cache_creation_1h_tokens
		END`

// standardCost is a row's cost at the global price (the list price of a
// model without one), before price rules and the rate: usage_logs.base_cost
// (CONTRACTS §97). A row from before that column has the charge divided by
// its multiplier, where a free group (multiplier 0) charged nothing and
// reports nothing.
const standardCost = `COALESCE(u.base_cost, CASE WHEN u.rate_multiplier > 0 THEN u.total_cost / u.rate_multiplier ELSE u.total_cost END)`

// Totals is one window of GET /me/usage/overview.
type Totals struct {
	Requests            int64           `json:"requests"`
	Success             int64           `json:"success"`
	InputTokens         int64           `json:"input_tokens"`
	OutputTokens        int64           `json:"output_tokens"`
	CacheReadTokens     int64           `json:"cache_read_tokens"`
	CacheCreationTokens int64           `json:"cache_creation_tokens"`
	PromptTokens        *int64          `json:"prompt_tokens"`
	TotalCost           decimal.Decimal `json:"total_cost"`
	StandardCost        decimal.Decimal `json:"standard_cost"`
	LatencyMsAvg        int64           `json:"latency_ms_avg"`
	FirstTokenMsAvg     int64           `json:"first_token_ms_avg"`
}

// Overview is GET /me/usage/overview and GET /usage/overview: the dashboard's
// figures in one answer. APIKeys is the caller's (self view only); Users and
// Accounts are the operator's, each only with the permission to read them.
type Overview struct {
	TZ        string `json:"tz"`
	Today     Totals `json:"today"`
	Yesterday Totals `json:"yesterday"`
	Last30d   Totals `json:"last_30d"`
	// Last5m gives the requests and tokens per minute (divide by 5).
	Last5m struct {
		Requests int64 `json:"requests"`
		Tokens   int64 `json:"tokens"`
	} `json:"last_5m"`
	APIKeys *struct {
		Total  int64 `json:"total"`
		Active int64 `json:"active"`
	} `json:"api_keys,omitempty"`
	Users *struct {
		Total       int64 `json:"total"`
		NewToday    int64 `json:"new_today"`
		ActiveToday int64 `json:"active_today"`
	} `json:"users,omitempty"`
	Accounts *account.Counts `json:"accounts,omitempty"`
}

// totalsColumns are the aggregates of Totals over the statRows matching cond
// ("": all). The prompt tokens are known when every row has them; over no
// rows they are null with nullPrompt (the summary), else 0 (the overview).
func totalsColumns(cond string, nullPrompt bool) string {
	promptSum := sumOf("u.prompt_tokens", cond)
	if nullPrompt {
		promptSum = `sum(u.prompt_tokens)` + filterWhere(cond)
	}
	return strings.Join([]string{
		sumOf("u.requests", cond), sumOf("u.success", cond),
		sumOf("u.input_tokens", cond), sumOf("u.output_tokens", cond),
		sumOf("u.cache_read_tokens", cond), sumOf("u.cache_write_tokens", cond),
		`CASE WHEN ` + sumOf("u.requests", cond) + ` = ` + sumOf("u.prompt_rows", cond) + ` THEN ` + promptSum + ` END`,
		sumOf("u.total_cost", cond), sumOf("u.standard_cost", cond),
		avgOf("u.latency_sum", "u.latency_rows", cond), avgOf("u.first_token_sum", "u.first_token_rows", cond),
	}, ", ")
}

func (t *Totals) scanTargets() []any {
	return []any{&t.Requests, &t.Success, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheCreationTokens,
		&t.PromptTokens, &t.TotalCost, &t.StandardCost, &t.LatencyMsAvg, &t.FirstTokenMsAvg}
}

// GET /me/usage/overview?tz=: the caller's today and yesterday, last 30 days
// (calendar days in tz), last 5 minutes and API keys, from one scan of the
// caller's last 30 days.
func (s *Service) myOverview(c *gin.Context) {
	uid, _ := core.UserID(c.Request.Context())
	s.overview(c, &uid)
}

// GET /usage/overview?tz= (usage:all:read): the same windows over every user,
// plus users (user:read) and accounts (account:read). Cached per time zone and
// permission set (CONTRACTS §66.3): it scans everyone's last 30 days.
func (s *Service) allOverview(c *gin.Context) { s.overview(c, nil) }

func (s *Service) overview(c *gin.Context, self *int64) {
	ctx := c.Request.Context()
	tz, loc, err := parseTZ(c)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	var out Overview
	if self != nil {
		out, err = s.loadOverview(ctx, tz, loc, overviewOf{self: self})
	} else {
		var users, accounts, hit bool
		if users, err = core.Can(ctx, s.Authz, "user:read"); err == nil {
			accounts, err = s.seesAccounts(ctx, nil)
		}
		if err == nil {
			of := overviewOf{users: users}
			if accounts {
				of.accounts = core.AllScope
			}
			out, hit, err = s.overviews.get(ctx, fmt.Sprintf("%s|%t|%t", tz, users, accounts), func(ctx context.Context) (Overview, error) {
				return s.loadOverview(ctx, tz, loc, of)
			})
			c.Header("X-Snapshot-Cache", snapshotHeader(hit))
		}
	}
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// overviewOf is what one overview covers.
type overviewOf struct {
	self  *int64 // the caller's own rows, and its API keys
	users bool   // the users section
	// accounts is the accounts section, over these accounts; none when it
	// reaches nothing. scoped keeps the rows they served alone (the account
	// side, inScope).
	accounts core.DataScope
	scoped   bool
}

// scopeKey identifies the rows of s in a snapshot cache key: callers of
// different scopes never share an answer. It names the scope, not the rows
// it reaches: a move of a user or an account between owners shows in a
// cached answer once that expires (CONTRACTS §103.2).
func scopeKey(s core.DataScope) string {
	return fmt.Sprint(s.All, s.Owners, s.Teams, s.Groups, s.IDs, s.PairG, s.PairID)
}

func (s *Service) loadOverview(ctx context.Context, tz string, loc *time.Location, of overviewOf) (Overview, error) {
	w := overviewWindows(overviewNow(), loc)
	out := Overview{TZ: tz}
	var activeToday int64
	err := s.statsTx(ctx, statsLimitsFor(of.self), func(tx *gorm.DB) error {
		g := store.G(ctx, tx)
		var f store.Conds
		f.And("u.created_at >= ?", w.last30d)
		if of.self != nil {
			f.And("u.user_id = ?", *of.self)
		}
		if of.scoped {
			inScope(&f, g, of.accounts)
		}
		span, err := rollupSpan(ctx, tx, w.last30d, time.Now(), loc)
		if err != nil {
			return err
		}
		targets := append(append(append(out.Today.scanTargets(), out.Yesterday.scanTargets()...), out.Last30d.scanTargets()...),
			&out.Last5m.Requests, &out.Last5m.Tokens, &activeToday)
		// Named parameters: each window bound is used several times.
		if err := g.Table("?", statRows(g, clause.Expr{SQL: `u.created_at AS at, u.user_id`}, ``, f, span)).Select(
			totalsColumns(`u.at >= @today`, false)+`, `+
				totalsColumns(`u.at >= @yesterday AND u.at < @today`, false)+`, `+
				totalsColumns(``, false)+`, `+
				sumOf(`u.requests`, `u.at >= @last5m`)+`, `+
				sumOf(`u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens`, `u.at >= @last5m`)+`,
			count(DISTINCT u.user_id) FILTER (WHERE u.at >= @today)`, map[string]any{
				"today": w.today, "yesterday": w.yesterday, "last5m": w.last5m,
			}).Row().Scan(targets...); err != nil {
			return err
		}
		if of.self != nil {
			out.APIKeys = &struct {
				Total  int64 `json:"total"`
				Active int64 `json:"active"`
			}{}
			// Active ones are also unexpired.
			return g.Model(&model.APIKey{}).
				Select(`count(*), count(*) FILTER (WHERE status = 'active' AND (expires_at IS NULL OR expires_at > now()))`).
				Where("user_id = ? AND deleted_at IS NULL", *of.self).Row().Scan(&out.APIKeys.Total, &out.APIKeys.Active)
		}
		if of.users {
			out.Users = &struct {
				Total       int64 `json:"total"`
				NewToday    int64 `json:"new_today"`
				ActiveToday int64 `json:"active_today"`
			}{}
			out.Users.ActiveToday = activeToday
			if err := g.Model(&model.User{}).Select(`count(*), count(*) FILTER (WHERE created_at >= ?)`, w.today).Where("deleted_at IS NULL").Row().Scan(&out.Users.Total, &out.Users.NewToday); err != nil {
				return err
			}
		}
		if !of.accounts.Empty() {
			counts, err := account.CountAccounts(ctx, tx, of.accounts)
			if err != nil {
				return err
			}
			out.Accounts = &counts
		}
		return nil
	})
	return out, err
}

// Realtime is GET /usage/realtime and GET /me/usage/realtime: the last minute
// of the usage records page (CONTRACTS §66.7), as new-api's log stat counts it.
// RPM and TPM are the requests that succeeded; with RecentFailures they give
// the last-minute success rate.
type Realtime struct {
	RPM            int64 `json:"rpm"`
	TPM            int64 `json:"tpm"`
	RecentFailures int64 `json:"recent_failures"`
}

const (
	// realtimeWindow is the window of Realtime, over when a request finished.
	realtimeWindow = 60 * time.Second
	// realtimeScan bounds the rows looked at. created_at is when a request
	// started, so the window cannot use it directly; a request running longer
	// than this leaves the numbers.
	realtimeScan = time.Hour
)

// realtimeTokens is core.UsageTokens.Throughput in SQL: the input outside any
// cache plus the output.
const realtimeTokens = `CASE WHEN u.billing_detail->'inputs'->>'semantics' = 'inclusive'
		THEN GREATEST(u.input_tokens - u.cache_read_tokens - u.cache_creation_tokens - u.cache_creation_1h_tokens, 0)
		ELSE u.input_tokens END + u.output_tokens`

// realtimeFailure leaves out what says nothing about the service, as new-api
// does: a 400, the client going away, and a request the gateway itself turned
// down before trying an account (gateway/errors.go).
const realtimeFailure = `NOT u.success AND u.status_code <> 400 AND u.error_type NOT IN
		('client_canceled', 'rate_limited', 'invalid_request', 'insufficient_balance',
		 'key_quota_exhausted', 'model_not_allowed', 'blocked_by_hook')`

func (s *Service) myRealtime(c *gin.Context) {
	uid, _ := core.UserID(c.Request.Context())
	s.realtime(c, &uid)
}

func (s *Service) allRealtime(c *gin.Context) { s.realtime(c, nil) }

// realtime takes the filters of the usage list. The console leaves out the
// time range and the status: the numbers are always the last minute's, and
// they need both outcomes.
func (s *Service) realtime(c *gin.Context, self *int64) {
	accounts, err := s.seesAccounts(c.Request.Context(), self)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	f, err := parseFilter(c, self, trailViewFor(self, accounts))
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	s.serveRealtime(c, f)
}

// serveRealtime answers the realtime figures of the records f keeps.
func (s *Service) serveRealtime(c *gin.Context, f store.Conds) {
	ctx := c.Request.Context()
	now := time.Now()
	f.And("u.created_at >= ?", now.Add(-realtimeScan))
	f.And("u.created_at + u.latency_ms * interval '1 millisecond' >= ?", now.Add(-realtimeWindow))
	// Token counting is free and takes no part in rate limits (§18).
	f.And("u.protocol NOT LIKE '%.count_tokens'")
	var out Realtime
	err := s.statsTx(ctx, directStats, func(tx *gorm.DB) error {
		return store.G(ctx, tx).Table("usage_logs u").Select(`count(*) FILTER (WHERE u.success),
			COALESCE(sum(`+realtimeTokens+`) FILTER (WHERE u.success), 0),
			count(*) FILTER (WHERE `+realtimeFailure+`)`).Scopes(f.Scope()).
			Row().Scan(&out.RPM, &out.TPM, &out.RecentFailures)
	})
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	httpapi.OK(c, out)
}

// overviewNow is the clock of the overview's calendar windows; a test fixes
// it so that a run across midnight sees the windows it seeded.
var overviewNow = time.Now

type windows struct{ today, yesterday, last30d, last5m time.Time }

// overviewWindows gives the lower bounds of the overview's windows. Days are
// calendar days in loc (the last 30 are today and the 29 before it, as the
// console's "last 30 days" range); AddDate keeps a DST day at its real length.
func overviewWindows(now time.Time, loc *time.Location) windows {
	l := now.In(loc)
	today := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	return windows{today: today, yesterday: today.AddDate(0, 0, -1), last30d: today.AddDate(0, 0, -29), last5m: now.Add(-5 * time.Minute)}
}

// rankRows applies ?order=cost|requests|tokens&limit=N to grouped summary
// rows: the largest first, the rest folded into one row keyed "__other__".
// Without either parameter the rows are returned unchanged.
func rankRows(order, limitArg string, rows []SummaryRow) ([]SummaryRow, error) {
	if order == "" && limitArg == "" {
		return rows, nil
	}
	metric := map[string]func(SummaryRow) decimal.Decimal{
		"cost":     func(r SummaryRow) decimal.Decimal { return r.TotalCost },
		"requests": func(r SummaryRow) decimal.Decimal { return decimal.NewFromInt(r.Requests) },
		"tokens":   func(r SummaryRow) decimal.Decimal { return decimal.NewFromInt(r.TotalTokens) },
	}[cmp.Or(order, "cost")]
	if metric == nil {
		return nil, core.ErrInvalidArgument.WithMessage("order must be cost, requests or tokens")
	}
	limit := 200
	if limitArg != "" {
		n, err := strconv.Atoi(limitArg)
		if err != nil || n < 1 || n > 200 {
			return nil, core.ErrInvalidArgument.WithMessage("limit must be 1-200")
		}
		limit = n
	}
	slices.SortStableFunc(rows, func(a, b SummaryRow) int {
		if d := metric(b).Cmp(metric(a)); d != 0 {
			return d
		}
		return strings.Compare(a.Key, b.Key)
	})
	if len(rows) <= limit {
		return rows, nil
	}
	other := SummaryRow{Key: "__other__", PromptTokens: new(int64)}
	var latencyWeight, firstWeight int64
	for _, r := range rows[limit:] {
		other.Requests += r.Requests
		other.Success += r.Success
		other.InputTokens += r.InputTokens
		other.OutputTokens += r.OutputTokens
		other.CacheReadTokens += r.CacheReadTokens
		other.CacheCreationTokens += r.CacheCreationTokens
		other.TotalTokens += r.TotalTokens
		other.TotalCost = other.TotalCost.Add(r.TotalCost)
		other.StandardCost = other.StandardCost.Add(r.StandardCost)
		other.EstimatedCost = other.EstimatedCost.Add(r.EstimatedCost)
		other.Anomalies += r.Anomalies
		other.Estimated += r.Estimated
		if other.PromptTokens != nil && r.PromptTokens != nil {
			*other.PromptTokens += *r.PromptTokens
		} else {
			other.PromptTokens = nil
		}
		// Averages over successful requests, weighted by them.
		other.LatencyMsAvg += r.LatencyMsAvg * r.Success
		other.FirstTokenMsAvg += r.FirstTokenMsAvg * r.Success
		if r.LatencyMsAvg > 0 {
			latencyWeight += r.Success
		}
		if r.FirstTokenMsAvg > 0 {
			firstWeight += r.Success
		}
	}
	if latencyWeight > 0 {
		other.LatencyMsAvg /= latencyWeight
	}
	if firstWeight > 0 {
		other.FirstTokenMsAvg /= firstWeight
	}
	return append(rows[:limit:limit], other), nil
}
