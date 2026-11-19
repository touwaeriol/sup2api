package payment

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	pluginv1 "github.com/Sub2API-Devs/sup2api/next/sdk/gen/pluginv1"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk"
	"github.com/Sub2API-Devs/sup2api/next/sdk/pluginsdk/pluginsdktest"
	"github.com/shopspring/decimal"
)

func TestPlugin_OrderCreationAndFulfillment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	p, fakeHost := startDB(t)

	ctx := context.Background()

	// Create test order
	req := CreateOrderRequest{
		UserID:      1001,
		Amount:      decimal.NewFromInt(100),
		PaymentType: PaymentTypeAlipay,
		OrderType:   OrderTypeBalance,
		ClientIP:    "127.0.0.1",
	}

	order, err := p.orderService.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	if order.OrderID == 0 {
		t.Errorf("Expected order ID > 0, got %d", order.OrderID)
	}
	if order.OutTradeNo == "" {
		t.Error("Expected non-empty out_trade_no")
	}
	if order.PayURL == "" {
		t.Error("Expected non-empty pay_url")
	}

	t.Logf("Order created: ID=%d, OutTradeNo=%s", order.OrderID, order.OutTradeNo)

	// Simulate payment notification
	notification := &PaymentNotification{
		OutTradeNo: order.OutTradeNo,
		TradeNo:    "test_trade_12345",
		Amount:     decimal.NewFromInt(100),
		Status:     NotificationStatusSuccess,
	}

	// Mock: directly update order and execute fulfillment
	err = mustDB(t, p).Exec(`
		UPDATE payment_orders
		SET status = ?, payment_trade_no = ?, paid_at = ?
		WHERE out_trade_no = ?
	`, StatusPaid, notification.TradeNo, time.Now(), notification.OutTradeNo).Error
	if err != nil {
		t.Fatalf("Update order to paid failed: %v", err)
	}

	// Execute fulfillment
	err = p.orderService.executeFulfillment(ctx, order.OrderID, req.UserID, req.Amount)
	if err != nil {
		t.Fatalf("Execute fulfillment failed: %v", err)
	}

	// Verify order status is completed
	var status string
	err = mustDB(t, p).Raw(`SELECT status FROM payment_orders WHERE id = ?`, order.OrderID).Row().Scan(&status)
	if err != nil {
		t.Fatalf("Query order status failed: %v", err)
	}
	if status != StatusCompleted {
		t.Errorf("Expected order status %s, got %s", StatusCompleted, status)
	}

	// Verify ledger credit was called
	ledgerCalls := hostCredits(fakeHost)
	if len(ledgerCalls) == 0 {
		t.Fatal("Expected ledger credit call, got none")
	}

	call := ledgerCalls[0]
	if call.GetUserId() != req.UserID {
		t.Errorf("Expected user_id %d, got %d", req.UserID, call.GetUserId())
	}
	if call.GetAmount() != req.Amount.String() {
		t.Errorf("Expected amount %s, got %s", req.Amount.String(), call.GetAmount())
	}
	if call.GetRefType() != "payment_order" {
		t.Errorf("Expected ref_type 'payment_order', got %s", call.GetRefType())
	}

	t.Log("Order fulfillment test passed")
}

func TestPlugin_RedeemCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	p, fakeHost := startDB(t)

	ctx := context.Background()

	// Generate redeem codes
	req := GenerateCodesRequest{
		Count: 3,
		Type:  RedeemTypeBalance,
		Value: decimal.NewFromInt(50),
		Notes: "Test codes",
	}

	codes, err := p.redeemService.GenerateCodes(ctx, req)
	if err != nil {
		t.Fatalf("GenerateCodes failed: %v", err)
	}

	if len(codes) != 3 {
		t.Fatalf("Expected 3 codes, got %d", len(codes))
	}

	for _, c := range codes {
		if c.Code == "" {
			t.Error("Expected non-empty code")
		}
		if c.CodeHash == "" {
			t.Error("Expected non-empty code hash")
		}
		if c.Status != RedeemStatusUnused {
			t.Errorf("Expected status %s, got %s", RedeemStatusUnused, c.Status)
		}
	}

	t.Logf("Generated %d redeem codes", len(codes))

	// Redeem first code
	userID := int64(2001)
	redeemedCode, err := p.redeemService.RedeemCode(ctx, userID, codes[0].Code)
	if err != nil {
		t.Fatalf("RedeemCode failed: %v", err)
	}

	if redeemedCode.Status != RedeemStatusUsed {
		t.Errorf("Expected status %s, got %s", RedeemStatusUsed, redeemedCode.Status)
	}
	if redeemedCode.UsedBy == nil || *redeemedCode.UsedBy != userID {
		t.Error("Expected code to be marked as used by user")
	}

	// Verify ledger credit
	ledgerCalls := hostCredits(fakeHost)
	if len(ledgerCalls) == 0 {
		t.Fatal("Expected ledger credit call")
	}

	call := ledgerCalls[0]
	if call.GetUserId() != userID {
		t.Errorf("Expected user_id %d, got %d", userID, call.GetUserId())
	}
	if call.GetAmount() != codes[0].Value.String() {
		t.Errorf("Expected amount %s, got %s", codes[0].Value.String(), call.GetAmount())
	}
	if call.GetRefType() != "redeem_code" {
		t.Errorf("Expected ref_type 'redeem_code', got %s", call.GetRefType())
	}

	// Try to redeem same code again (should fail)
	_, err = p.redeemService.RedeemCode(ctx, userID, codes[0].Code)
	if err == nil {
		t.Error("Expected error when redeeming used code, got nil")
	}

	t.Log("Redeem code test passed")
}

func TestPlugin_RedeemCodeConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	p, _ := startDB(t)

	ctx := context.Background()

	// Generate one code
	codes, err := p.redeemService.GenerateCodes(ctx, GenerateCodesRequest{
		Count: 1,
		Type:  RedeemTypeBalance,
		Value: decimal.NewFromInt(100),
	})
	if err != nil {
		t.Fatalf("GenerateCodes failed: %v", err)
	}

	code := codes[0].Code

	// Try to redeem concurrently by different users
	type result struct {
		userID int64
		err    error
	}

	results := make(chan result, 5)

	for i := 0; i < 5; i++ {
		userID := int64(3000 + i)
		go func(uid int64) {
			_, err := p.redeemService.RedeemCode(ctx, uid, code)
			results <- result{userID: uid, err: err}
		}(userID)
	}

	// Collect results
	successCount := 0
	var successUserID int64

	for i := 0; i < 5; i++ {
		res := <-results
		if res.err == nil {
			successCount++
			successUserID = res.userID
		}
	}

	// Only one should succeed
	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful redemption, got %d", successCount)
	}

	// Verify the code is marked as used by the successful user
	var usedBy *int64
	var status string
	err = mustDB(t, p).Raw(`
		SELECT status, used_by FROM redeem_codes WHERE code_hash = ?
	`, HashRedeemCode(code)).Row().Scan(&status, &usedBy)
	if err != nil {
		t.Fatalf("Query code failed: %v", err)
	}

	if status != RedeemStatusUsed {
		t.Errorf("Expected status %s, got %s", RedeemStatusUsed, status)
	}
	if usedBy == nil || *usedBy != successUserID {
		t.Errorf("Expected used_by %d, got %v", successUserID, usedBy)
	}

	t.Log("Concurrent redeem test passed")
}

func TestPlugin_PromoCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	p, fakeHost := startDB(t)

	ctx := context.Background()

	// Create promo code
	req := CreatePromoCodeRequest{
		Code:        "WELCOME2024",
		BonusAmount: decimal.NewFromInt(20),
		MaxUses:     2,
	}

	promo, err := p.promoService.CreatePromoCode(ctx, req)
	if err != nil {
		t.Fatalf("CreatePromoCode failed: %v", err)
	}

	if promo.Code != req.Code {
		t.Errorf("Expected code %s, got %s", req.Code, promo.Code)
	}
	if !promo.BonusAmount.Equal(req.BonusAmount) {
		t.Errorf("Expected bonus %s, got %s", req.BonusAmount.String(), promo.BonusAmount.String())
	}

	t.Logf("Promo code created: %s", promo.Code)

	// Apply promo code - first user
	userID1 := int64(4001)
	err = p.promoService.ApplyPromoCode(ctx, userID1, promo.Code)
	if err != nil {
		t.Fatalf("ApplyPromoCode (user 1) failed: %v", err)
	}

	// Apply promo code - second user
	userID2 := int64(4002)
	err = p.promoService.ApplyPromoCode(ctx, userID2, promo.Code)
	if err != nil {
		t.Fatalf("ApplyPromoCode (user 2) failed: %v", err)
	}

	// Apply promo code - third user (should fail due to max_uses)
	userID3 := int64(4003)
	err = p.promoService.ApplyPromoCode(ctx, userID3, promo.Code)
	if err == nil {
		t.Error("Expected error when exceeding max_uses, got nil")
	}

	// Try to apply same code again by first user (should fail)
	err = p.promoService.ApplyPromoCode(ctx, userID1, promo.Code)
	if err == nil {
		t.Error("Expected error when user applies same code twice, got nil")
	}

	// Verify ledger credits (should be 2)
	ledgerCalls := hostCredits(fakeHost)
	if len(ledgerCalls) != 2 {
		t.Fatalf("Expected 2 ledger credit calls, got %d", len(ledgerCalls))
	}

	for i, call := range ledgerCalls {
		if call.GetAmount() != promo.BonusAmount.String() {
			t.Errorf("Call %d: expected amount %s, got %s", i, promo.BonusAmount.String(), call.GetAmount())
		}
		if call.GetRefType() != "promo_code" {
			t.Errorf("Call %d: expected ref_type 'promo_code', got %s", i, call.GetRefType())
		}
	}

	t.Log("Promo code test passed")
}

func TestPlugin_AmountPrecision(t *testing.T) {
	// Test decimal precision in order creation
	amount := decimal.NewFromFloat(99.99)
	feeRate := decimal.NewFromFloat(0.5) // 0.5%

	fee := amount.Mul(feeRate).Div(decimal.NewFromInt(100)).Round(2)
	payAmount := amount.Add(fee)

	expectedFee := decimal.NewFromFloat(0.50)
	expectedPayAmount := decimal.NewFromFloat(100.49)

	if !fee.Equal(expectedFee) {
		t.Errorf("Expected fee %s, got %s", expectedFee.String(), fee.String())
	}

	if !payAmount.Equal(expectedPayAmount) {
		t.Errorf("Expected pay_amount %s, got %s", expectedPayAmount.String(), payAmount.String())
	}

	t.Log("Amount precision test passed")
}

func TestPlugin_PaymentNotificationAmountMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	p, _ := startDB(t)

	ctx := context.Background()

	// Create order
	req := CreateOrderRequest{
		UserID:      5001,
		Amount:      decimal.NewFromInt(100),
		PaymentType: PaymentTypeAlipay,
		OrderType:   OrderTypeBalance,
	}

	order, err := p.orderService.CreateOrder(ctx, req)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}

	// Update to paid
	err = mustDB(t, p).Exec(`
		UPDATE payment_orders SET status = ?, payment_trade_no = ?
		WHERE id = ?
	`, StatusPaid, "test_trade", order.OrderID).Error
	if err != nil {
		t.Fatalf("Update order failed: %v", err)
	}

	// Try to fulfill with wrong amount (should be rejected by amount verification in real notification handler)
	// Here we test the tolerance logic
	wrongAmount := decimal.NewFromFloat(99.98) // More than 0.01 CNY diff
	diff := wrongAmount.Sub(req.Amount).Abs()

	tolerance := decimal.NewFromFloat(AmountToleranceCNY)
	if !diff.GreaterThan(tolerance) {
		t.Errorf("Expected diff %s to be greater than tolerance %s", diff.String(), tolerance.String())
	}

	// Amount within tolerance should pass
	acceptableAmount := decimal.NewFromFloat(100.005)
	diff = acceptableAmount.Sub(req.Amount).Abs()
	if diff.GreaterThan(tolerance) {
		t.Errorf("Expected diff %s to be within tolerance %s", diff.String(), tolerance.String())
	}

	t.Log("Amount mismatch test passed")
}

// startDB starts the plugin against a fresh schema of TEST_DATABASE_URL
// (skipped when unset) with the package migrations applied.
func startDB(t *testing.T) (*Plugin, *pluginsdktest.FakeHost) {
	t.Helper()
	dsn, schema := pluginsdktest.NewSchema(t, "plg_payment_t")
	pluginsdktest.ApplyMigrations(t, dsn, schema, filepath.Join("..", "..", "migrations"))
	fh := pluginsdktest.NewFakeHost()
	fh.SetDSN(dsn, schema)
	p := New()
	pluginsdktest.Start(t, p, pluginsdktest.Options{
		Host: fh,
		Grants: []*pluginv1.Grant{
			{Permission: "ledger.credit", ScopeJson: `{"maxPerTx":"100000","maxPerDay":"1000000"}`},
		},
		SDK: []pluginsdk.Option{pluginsdk.WithInfo("payment", "0.1.3")},
	})
	return p, fh
}

// hostCredits returns the ledger.credit requests the fake host received.
func hostCredits(fh *pluginsdktest.FakeHost) []*pluginv1.LedgerChangeRequest {
	var out []*pluginv1.LedgerChangeRequest
	for _, e := range fh.Ledger() {
		if e.Credit {
			out = append(out, e.Req)
		}
	}
	return out
}

func mustDB(t *testing.T, p *Plugin) *gorm.DB {
	t.Helper()
	db, err := p.db(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return db
}
