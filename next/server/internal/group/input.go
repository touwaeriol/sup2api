package group

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/core"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
)

type groupInput struct {
	Name            *string   `json:"name"`
	Description     *string   `json:"description"`
	Status          *string   `json:"status"`
	RateMultiplier  *string   `json:"rate_multiplier"`
	Visibility      *string   `json:"visibility"`
	ModelAllowlist  *[]string `json:"model_allowlist"`
	ModelFilterMode *string   `json:"model_filter_mode"`
	// Environment replaces the group layer as a whole.
	Environment *core.EnvironmentValues `json:"environment"`
	// ConversionLossPolicy sets or, with null, clears the policy (§88).
	ConversionLossPolicy core.LossPolicyInput `json:"conversion_loss_policy"`
}

// fieldError is the field error with the message in the caller's language.
func fieldError(ctx context.Context, field, code, en, zh string) core.FieldError {
	return core.FieldError{Field: field, Code: code, Message: t(ctx, en, zh)}
}

// check checks the provided fields, normalizing them as stored; create
// requires name. The field errors come in the order of the fields.
func (in *groupInput) check(ctx context.Context, create bool) []core.FieldError {
	fe := in.checkText(ctx, create)
	fe = append(fe, in.checkChoices(ctx)...)
	fe = append(fe, in.checkRate(ctx)...)
	fe = append(fe, in.checkModels(ctx)...)
	if in.Environment != nil {
		values, errs := core.ValidateEnvironmentValues(ctx, core.EnvLayerGroup, "environment", *in.Environment)
		fe = append(fe, errs...)
		in.Environment = &values
	}
	return append(fe, in.ConversionLossPolicy.Validate(ctx, "conversion_loss_policy")...)
}

// checkText checks the name, trimmed, and the description.
func (in *groupInput) checkText(ctx context.Context, create bool) []core.FieldError {
	var fe []core.FieldError
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		in.Name = &n
		if n == "" || utf8.RuneCountInString(n) > 100 {
			fe = append(fe, fieldError(ctx, "name", "invalid", "name must be 1-100 characters", "名称长度须为 1-100 个字符"))
		}
	} else if create {
		fe = append(fe, fieldError(ctx, "name", "required", "name is required", "名称必填"))
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 2000 {
		fe = append(fe, fieldError(ctx, "description", "too_long", "description is too long", "描述过长"))
	}
	return fe
}

// checkChoices checks the status and the visibility.
func (in *groupInput) checkChoices(ctx context.Context) []core.FieldError {
	var fe []core.FieldError
	if in.Status != nil && *in.Status != "active" && *in.Status != "disabled" {
		fe = append(fe, fieldError(ctx, "status", "invalid", "status must be active or disabled", "状态必须为 active 或 disabled"))
	}
	if in.Visibility != nil && *in.Visibility != "public" && *in.Visibility != "restricted" {
		fe = append(fe, fieldError(ctx, "visibility", "invalid", "visibility must be public or restricted", "可见性必须为 public 或 restricted"))
	}
	return fe
}

// checkRate checks the rate multiplier and normalizes a valid one.
func (in *groupInput) checkRate(ctx context.Context) []core.FieldError {
	if in.RateMultiplier == nil {
		return nil
	}
	d, ok := core.ParseRate(*in.RateMultiplier)
	if !ok {
		return []core.FieldError{fieldError(ctx, "rate_multiplier", "invalid",
			"rate multiplier must be a non-negative number with at most 4 decimals", "倍率必须为非负数，最多 4 位小数")}
	}
	v := d.String()
	in.RateMultiplier = &v
	return nil
}

// checkModels checks the model filter mode and the model patterns, trimmed.
func (in *groupInput) checkModels(ctx context.Context) []core.FieldError {
	var fe []core.FieldError
	if in.ModelFilterMode != nil && *in.ModelFilterMode != "whitelist" && *in.ModelFilterMode != "blacklist" {
		fe = append(fe, fieldError(ctx, "model_filter_mode", "invalid", "model filter mode must be whitelist or blacklist", "模型过滤模式必须为白名单或黑名单"))
	}
	if in.ModelAllowlist == nil {
		return fe
	}
	clean := make([]string, 0, len(*in.ModelAllowlist))
	for _, m := range *in.ModelAllowlist {
		m = strings.TrimSpace(m)
		if m == "" {
			fe = append(fe, fieldError(ctx, "model_allowlist", "invalid", "model patterns must not be empty", "模型模式不能为空"))
			break
		}
		clean = append(clean, m)
	}
	in.ModelAllowlist = &clean
	return fe
}

// columns are the groups columns a PATCH writes: the fields present, as
// written, and updated_at = now(), as the raw SQL had it.
func (in *groupInput) columns() map[string]any {
	set := map[string]any{"updated_at": gorm.Expr("now()")}
	store.SetIf(set, "name", in.Name)
	store.SetIf(set, "description", in.Description)
	store.SetIf(set, "status", in.Status)
	store.SetIf(set, "rate_multiplier", in.RateMultiplier)
	store.SetIf(set, "visibility", in.Visibility)
	store.SetIf(set, "model_filter_mode", in.ModelFilterMode)
	if in.ModelAllowlist != nil {
		b, _ := json.Marshal(*in.ModelAllowlist)
		set["model_allowlist"] = string(b)
	}
	store.SetIf(set, "environment", in.Environment)
	if in.ConversionLossPolicy.Set {
		set["conversion_loss_policy"] = in.ConversionLossPolicy.Stored()
	}
	return set
}
