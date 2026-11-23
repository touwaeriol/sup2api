// Package lookup serves GET /lookup/:kind (CONTRACTS §96): the console's
// entity picker finds users, groups, API keys, accounts, models and proxies
// by name or id, and turns ids back into names.
//
// The protocol (parameters, matching, order, response) is implemented once,
// here. A kind is an entity: where its rows are and who may see them
// (entities.go). A new kind is one more entity.
package lookup

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/httpapi"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/perm"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

const (
	defaultLimit = 20
	maxLimit     = 50
	maxIDs       = 100
	maxText      = 256 // bytes of q, as the usage log's "contains" filters

	// contains matches a store.Like pattern, so %, _ and \ in q are literal.
	contains = ` ILIKE ? ESCAPE E'\\'`
)

// entity describes one kind of row. Columns and conditions are SQL over the
// aliases of from; conditions take "?" placeholders (store.Conds). The
// fields are unexported, set by the literals of entities.go alone, so that
// the SQL check proves their text constant.
type entity struct {
	kind string // path segment: /lookup/<kind>
	from string // FROM clause
	id   string // numeric id column; "" when the name is the id (models)
	name string // column q matches, returned as name
	// matches are further columns q matches, for a kind people know by more
	// than its name.
	matches []string
	hint    string // optional expression: text that tells rows of one name apart
	status  string // optional column, returned as stored
	live    string // optional condition leaving out soft-deleted rows

	// read reaches every row. ownRead (kinds without a table: groups, by
	// account:group:bind) reaches the caller's rows only: a holder of
	// ownRead alone is narrowed by mine instead of refused.
	read, ownRead string
	// named, when set, gives a caller holding neither read nor ownRead the
	// rows its own permission set names (by id), instead of a refusal: the
	// console shows the scopes of the caller's grants by name.
	named func(core.PermissionSet) []int64
	// table makes read a scoped action (CONTRACTS §21.0): the caller sees
	// the rows of its scope of read, any scope will do, and ownRead is
	// unused.
	table func() core.ScopeTable
	// mine serves mine=true; nil when no row of the kind is the caller's own.
	mine *ownRows

	narrow []ref // parameters that drop the rows not matching them
	prefer []ref // parameters that list the matching rows first
}

// ownRows is the part of a kind that belongs to the caller.
type ownRows struct {
	// perms allow mine=true, any one of them; none means every signed-in user.
	perms []string
	// scope adds the conditions that leave only the caller's rows.
	scope func(ctx context.Context, s *Service, uid int64, f *store.Conds) error
	// hint replaces entity.hint when set.
	hint string
}

// ref is a query parameter holding the id of another entity.
type ref struct {
	param string
	cond  string // true for the rows the id selects; one "?" takes the id
}

// key is the expression of the row's id.
func (e *entity) key() string {
	if e.id == "" {
		return e.name
	}
	return e.id
}

// Item is one row of the answer.
type Item struct {
	ID     any    `json:"id"` // a number; the model name for models
	Name   string `json:"name"`
	Hint   string `json:"hint,omitempty"`
	Status string `json:"status,omitempty"`
}

// itemRow is an Item as query reads it.
type itemRow struct {
	ID                 itemID
	Name, Hint, Status string
}

// itemID is the id column as the driver hands it: an int64, or the name of
// a model. GORM maps no interface field, hence the Scanner.
type itemID struct{ v any }

func (i *itemID) Scan(src any) error { i.v = src; return nil }

// GormDataType makes GORM map itemID as a column.
func (itemID) GormDataType() string { return "text" }

// Result is the answer of GET /lookup/:kind. More says that the limit cut
// further matches off.
type Result struct {
	Items []Item `json:"items"`
	More  bool   `json:"more"`
}

// Service serves the lookup endpoint.
type Service struct {
	db     *store.DB
	authz  core.Authorizer
	priced core.PricedModels // the models with a list price in a group (billing)
	kinds  map[string]*entity
}

// New builds the service over the kinds of entities.go.
func New(db *store.DB, authz core.Authorizer, priced core.PricedModels) *Service {
	s := &Service{db: db, authz: authz, priced: priced, kinds: make(map[string]*entity, len(entities))}
	for _, e := range entities {
		s.kinds[e.kind] = e
	}
	return s
}

// RegisterRoutes mounts the endpoint. The permission depends on the kind and
// on mine, so the handler checks it (allow); like the other read-only lists
// an access key may call it, within the key's own permissions.
func (s *Service) RegisterRoutes(r *httpapi.Router) {
	r.AuthedRead("/lookup/:kind", s.lookup)
}

func (s *Service) lookup(c *gin.Context) {
	e, ok := s.kinds[c.Param("kind")]
	if !ok {
		httpapi.Fail(c, core.ErrNotFound)
		return
	}
	p, err := parse(c, e)
	if err != nil {
		httpapi.Fail(c, err)
		return
	}
	a, ok := s.allow(c, e, p.mine)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	var f store.Conds
	if e.live != "" {
		f.And(e.live)
	}
	if a.named != nil {
		f.And(e.id+" = ANY(?)", store.Int64s(a.named))
	}
	if e.table != nil {
		scope, err := core.Scope(ctx, s.authz, perm.Key(e.read))
		if err == nil && scope.Empty() {
			err = core.ErrPermissionDenied.WithDetails(map[string]any{"permission": e.read})
		}
		if err != nil {
			httpapi.Fail(c, err)
			return
		}
		f = append(f, e.table().Scope(scope))
	}
	if a.own {
		uid, _ := core.UserID(ctx)
		if err := e.mine.scope(ctx, s, uid, &f); err != nil {
			httpapi.Fail(c, err)
			return
		}
	}
	var rows []itemRow
	if err := e.query(store.G(ctx, s.db.Gorm), p, f, a.own, &rows).Error; err != nil {
		httpapi.Fail(c, err)
		return
	}
	items := make([]Item, len(rows))
	for i, r := range rows {
		items[i] = Item{ID: r.ID.v, Name: r.Name, Hint: r.Hint, Status: r.Status}
	}
	// statement asked for one row more than the limit to learn this.
	more := p.ids == nil && len(items) > p.limit
	if more {
		items = items[:p.limit]
	}
	httpapi.OK(c, Result{Items: items, More: more})
}

// access is the caller's range of one lookup, beyond a Table's scope.
type access struct {
	own   bool    // narrowed by entity.mine
	named []int64 // narrowed to these ids (entity.named); nil: not by it
}

// allow checks the caller's permission for the kind and returns the range
// the lookup is limited to. When ok is false the error has been rendered.
func (s *Service) allow(c *gin.Context, e *entity, mine bool) (a access, ok bool) {
	if mine && e.mine == nil {
		httpapi.Fail(c, core.ErrInvalidArgument.WithMessage("mine is not supported for "+e.kind))
		return a, false
	}
	if e.table != nil { // checked and narrowed by its scope
		return access{own: mine}, true
	}
	if mine {
		return access{own: true}, len(e.mine.perms) == 0 || httpapi.CanAny(c, s.authz, e.mine.perms...)
	}
	ctx := c.Request.Context()
	if e.named != nil {
		read, err := core.Can(ctx, s.authz, perm.Key(e.read))
		if err == nil && !read {
			read, err = core.Can(ctx, s.authz, perm.Key(e.ownRead))
		}
		if err != nil {
			httpapi.Fail(c, err)
			return a, false
		}
		if !read {
			return s.allowNamed(c, e)
		}
	}
	if !httpapi.CanAny(c, s.authz, e.read, e.ownRead) {
		return a, false
	}
	// CanAny recorded the keys it matched in a new request context: not ctx.
	return access{own: !core.HasGranted(c.Request.Context(), perm.Key(e.read))}, true
}

// allowNamed limits the lookup to the rows e.named finds in the caller's
// permission set (its access key applied), refusing as Read would when it
// names none.
func (s *Service) allowNamed(c *gin.Context, e *entity) (access, bool) {
	ctx := c.Request.Context()
	uid, _ := core.UserID(ctx)
	set, err := s.authz.PermissionSet(ctx, uid)
	if err != nil {
		httpapi.Fail(c, err)
		return access{}, false
	}
	ids := e.named(set)
	if len(ids) == 0 {
		httpapi.Fail(c, core.ErrPermissionDenied.WithDetails(map[string]any{"permission": e.read}))
		return access{}, false
	}
	return access{named: ids}, true
}

// params are the parsed query parameters of one request.
type params struct {
	text  string // q
	id    *int64 // q read as an id: all digits, on a kind with numeric ids
	ids   any    // store.Int64s, or store.Strings for models; nil when not given
	limit int
	mine  bool
	refs  map[string]int64 // the given Narrow and Prefer parameters
}

func invalid(param string) error {
	return core.ErrInvalidArgument.WithMessage("invalid " + param)
}

// validText reports whether PostgreSQL takes s as text.
func validText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func parse(c *gin.Context, e *entity) (*params, error) {
	p := &params{text: strings.TrimSpace(c.Query("q")), limit: defaultLimit, refs: map[string]int64{}}
	if len(p.text) > maxText || !validText(p.text) {
		return nil, invalid("q")
	}
	// A q of digits is also an id. One too long for an id can still be part
	// of a name.
	if e.id != "" && p.text != "" && strings.Trim(p.text, "0123456789") == "" {
		if id, err := strconv.ParseInt(p.text, 10, 64); err == nil {
			p.id = &id
		}
	}
	if v := c.Query("ids"); v != "" {
		ids, err := parseIDs(v, e.id != "")
		if err != nil {
			return nil, err
		}
		p.ids = ids
	}
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			return nil, invalid("limit")
		}
		p.limit = n
	}
	if v := c.Query("mine"); v != "" {
		mine, err := strconv.ParseBool(v)
		if err != nil {
			return nil, invalid("mine")
		}
		p.mine = mine
	}
	for _, r := range slices.Concat(e.narrow, e.prefer) {
		v := c.Query(r.param)
		if v == "" {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return nil, invalid(r.param)
		}
		p.refs[r.param] = id
	}
	return p, nil
}

// TextQuery is the q, ids and limit of a lookup over text ids; IDs is nil
// when not given.
type TextQuery struct {
	Q     string
	IDs   []string
	Limit int
}

// ParseText reads a TextQuery as GET /lookup does for a kind without
// numeric ids: the plugin scope options proxy (CONTRACTS §96.1 rules) shares
// it.
func ParseText(c *gin.Context) (TextQuery, error) {
	p, err := parse(c, &entity{})
	if err != nil {
		return TextQuery{}, err
	}
	ids, _ := p.ids.(store.Strings)
	return TextQuery{Q: p.text, IDs: ids, Limit: p.limit}, nil
}

// parseIDs reads ids=a,b,c: positive integers, or names when the kind has no
// numeric id. They come as one array parameter each.
func parseIDs(v string, numeric bool) (any, error) {
	parts := strings.Split(v, ",")
	if len(parts) > maxIDs {
		return nil, core.ErrInvalidArgument.WithMessage("ids accepts at most " + strconv.Itoa(maxIDs) + " ids")
	}
	if !numeric {
		for i, part := range parts {
			parts[i] = strings.TrimSpace(part)
			if parts[i] == "" || !validText(parts[i]) {
				return nil, invalid("ids")
			}
		}
		return store.Strings(parts), nil
	}
	ids := make(store.Int64s, len(parts))
	for i, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, invalid("ids")
		}
		ids[i] = id
	}
	return ids, nil
}

// search adds the condition of q to f: the name or a Search column contains
// it, or it is the id. It returns the order that lists the row of that id
// first, with its argument.
func (e *entity) search(p *params, f *store.Conds) (string, []any) {
	pattern := "%" + store.Like(p.text) + "%"
	var conds []string
	var args []any
	for _, col := range slices.Concat([]string{e.name}, e.matches) {
		conds = append(conds, col+contains)
		args = append(args, pattern)
	}
	if p.id == nil {
		f.And("("+strings.Join(conds, " OR ")+")", args...)
		return "", nil
	}
	f.And("("+strings.Join(append(conds, e.id+" = ?"), " OR ")+")", append(args, *p.id)...)
	return "(" + e.id + " = ?) DESC", []any{*p.id}
}

// query reads into dst the rows of one request on top of f, which holds the
// conditions of the caller's range; own marks a request for the caller's
// own rows (mine), which take mine's hint. Order: the row whose id is q,
// the rows a Prefer parameter selects, then by name and id.
func (e *entity) query(db *gorm.DB, p *params, f store.Conds, own bool, dst *[]itemRow) *gorm.DB {
	for _, r := range e.narrow {
		if id, ok := p.refs[r.param]; ok {
			f.And(r.cond, id)
		}
	}
	var order []string
	var orderArgs []any
	switch {
	case p.ids != nil: // exactly these rows: q and limit do not apply
		f.And(e.key()+" = ANY(?)", p.ids)
	case p.text != "":
		if first, args := e.search(p, &f); first != "" {
			order, orderArgs = append(order, first), args
		}
	}
	for _, r := range e.prefer {
		if id, ok := p.refs[r.param]; ok {
			order = append(order, "("+r.cond+") DESC")
			orderArgs = append(orderArgs, id)
		}
	}
	order = append(order, e.name)
	if e.id != "" {
		order = append(order, e.id)
	}
	hint := e.hint
	if own && e.mine.hint != "" {
		hint = e.mine.hint
	}
	// The aliases are the columns of itemRow.
	db = db.Table(e.from).Select(e.key() + " AS id, " + e.name + " AS name, " + orEmpty(hint) + " AS hint, " + orEmpty(e.status) + " AS status").
		Scopes(f.Scope()).
		// One expression for the whole order: GORM renders an ORDER BY
		// expression in place of the columns of other Order calls.
		Order(clause.OrderBy{Expression: clause.Expr{SQL: strings.Join(order, ", "), Vars: orderArgs, WithoutParentheses: true}})
	if p.ids == nil {
		// One row more than asked tells whether the limit cut matches off.
		db = db.Limit(p.limit + 1)
	}
	return db.Scan(dst)
}

// orEmpty is the SQL of an optional text expression.
func orEmpty(expr string) string {
	if expr == "" {
		return "''"
	}
	return expr
}
