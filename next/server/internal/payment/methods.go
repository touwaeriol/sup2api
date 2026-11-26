package payment

import (
	"context"
	"slices"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/store"
	"github.com/Sub2API-Devs/sup2api/next/server/internal/store/model"
)

// Method is a payment method the administrator configured: a type key of
// one gateway, shown to users by name and icon, in list order.
type Method struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Gateway  string `json:"gateway"`
	Icon     string `json:"icon"`
	Color    string `json:"color"`
	Logo     string `json:"logo"`
	MinTopup Num    `json:"min_topup"`
	Enabled  bool   `json:"enabled"`
}

func methodOf(m model.PaymentMethod) Method {
	return Method{Type: m.Type, Name: m.Name, Gateway: m.Gateway, Icon: m.Icon, Color: m.Color, Logo: m.Logo,
		MinTopup: num(m.MinTopup), Enabled: m.Enabled == nil || *m.Enabled}
}

// loadMethods reads the payment methods in list order.
func loadMethods(ctx context.Context, q *gorm.DB) ([]Method, error) {
	var rows []model.PaymentMethod
	if err := store.G(ctx, q).Order("sort").Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Method, len(rows))
	for i, r := range rows {
		out[i] = methodOf(r)
	}
	return out, nil
}

// findMethod returns the method of type key, nil when there is none.
func findMethod(ms []Method, key string) *Method {
	i := slices.IndexFunc(ms, func(m Method) bool { return m.Type == key })
	if i < 0 {
		return nil
	}
	return &ms[i]
}

// replaceMethods makes the payment methods exactly ms, in their order:
// methods left out are deleted, the others written by type. Orders keep
// the method's type and name, so deleting a method loses nothing of them.
func replaceMethods(ctx context.Context, tx *gorm.DB, ms []Method) error {
	db := store.G(ctx, tx)
	types := make(store.Strings, len(ms))
	for i, m := range ms {
		types[i] = m.Type
	}
	if err := db.Where("NOT (type = ANY(?))", types).Delete(&model.PaymentMethod{}).Error; err != nil {
		return err
	}
	for i, m := range ms {
		enabled := m.Enabled
		row := model.PaymentMethod{Type: m.Type, Name: m.Name, Gateway: m.Gateway, Icon: m.Icon, Color: m.Color,
			Logo: m.Logo, MinTopup: m.MinTopup.Decimal, Enabled: &enabled, Sort: i}
		upsert := store.UpsertOn([]string{"type"}, "name", "gateway", "icon", "color", "logo", "min_topup", "enabled", "sort")
		if err := db.Clauses(upsert).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
