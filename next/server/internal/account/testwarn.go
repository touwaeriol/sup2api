package account

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
)

// TestWarning is a finding of an account test that does not fail it but
// tells the operator that client calls would still fail (CONTRACTS §122.5).
type TestWarning struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// warnPriceNotConfigured is the warning of a model without a price: the
// gateway refuses its calls with the same code (core.ErrPriceNotConfigured).
var warnPriceNotConfigured = core.ErrPriceNotConfigured.Code

// priceWarnings warns when client model model has no price in a group of
// account id (the global price only, for an account in no group): the test
// reaches the upstream, but the gateway would refuse the call (403). Nothing
// is checked without the PricedModels dependency or a model.
func (s *Service) priceWarnings(ctx context.Context, id int64, model string) []TestWarning {
	if s.d.Priced == nil || model == "" {
		return nil
	}
	missing, err := s.unpricedGroups(ctx, id, model)
	if err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "account: test price check", "account_id", id, "model", model, "err", err)
	}
	if err != nil || len(missing) == 0 {
		return nil
	}
	names := groupNames(missing)
	return []TestWarning{{Code: warnPriceNotConfigured, Message: priceWarningText(ctx, model, names),
		Details: map[string]any{"model": model, "groups": names}}}
}

// unpricedGroups lists the groups of account id where model has no price.
// An account in no group is checked as group 0, the global price.
func (s *Service) unpricedGroups(ctx context.Context, id int64, model string) ([]GroupRef, error) {
	refs, err := s.accountGroupRefs(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	groups := []GroupRef{{}}
	if len(refs) > 0 {
		groups = make([]GroupRef, len(refs))
		for i, r := range refs {
			groups[i] = GroupRef{ID: r.ID, Name: r.Name}
		}
	}
	ids := make([]int64, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	priced, err := s.d.Priced.PricedModels(ctx, ids, model)
	if err != nil {
		return nil, err
	}
	var missing []GroupRef
	for _, g := range groups {
		if len(priced[g.ID]) == 0 {
			missing = append(missing, g)
		}
	}
	return missing, nil
}

// groupNames are the names of groups, without group 0 (the global price).
func groupNames(groups []GroupRef) []string {
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		if g.ID != 0 {
			names = append(names, g.Name)
		}
	}
	return names
}

// priceWarningText is the message of the price warning, naming the groups
// without a price (none: the global price, for an account in no group).
func priceWarningText(ctx context.Context, model string, groups []string) string {
	if len(groups) == 0 {
		return fmt.Sprintf(t(ctx,
			"model %s has no price configured locally; client calls will be refused (403). Configure it in model prices first",
			"本地没有配置模型 %s 的价格，客户端调用会被拒绝（403），请先在模型价格里配置"), model)
	}
	return fmt.Sprintf(t(ctx,
		"model %s has no price configured locally in group %s; client calls there will be refused (403). Configure it in model prices first",
		"本地没有配置模型 %s 的价格（分组 %s），客户端调用会被拒绝（403），请先在模型价格里配置"), model, strings.Join(groups, t(ctx, ", ", "、")))
}

// lastTestMessage is the message a test leaves as the last test: for a
// failure the plugin's reason first, then what the upstream answered (the
// reason alone, "upstream server error (502)", hides the cause); for a
// success its warnings.
func lastTestMessage(res TestResult) string {
	if res.OK {
		texts := make([]string, len(res.Warnings))
		for i, w := range res.Warnings {
			texts[i] = w.Message
		}
		return cutBytes(strings.Join(texts, "\n"), maxLastTestMessage)
	}
	reason, upstream := strings.TrimSpace(res.Reason), strings.TrimSpace(res.Message)
	switch {
	case res.ErrorSource == "response" || reason == "":
		return cutBytes(upstream, maxLastTestMessage)
	case upstream == "" || strings.HasSuffix(reason, upstream):
		return cutBytes(reason, maxLastTestMessage)
	default:
		return cutBytes(reason+": "+upstream, maxLastTestMessage)
	}
}
