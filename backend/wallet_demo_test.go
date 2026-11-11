package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestValidateDemoWalletTransfer(t *testing.T) {
	cases := []struct {
		op   DemoWalletTransfer
		good bool
	}{
		{DemoWalletTransfer{Kind: "deposit", Amount: 1}, true},
		{DemoWalletTransfer{Kind: "withdraw", Amount: maxDemoWalletAmount}, true},
		{DemoWalletTransfer{Kind: "deposit", Amount: 0}, false},
		{DemoWalletTransfer{Kind: "withdraw", Amount: -1}, false},
		{DemoWalletTransfer{Kind: "deposit", Amount: maxDemoWalletAmount + 1}, false},
		{DemoWalletTransfer{Kind: "other", Amount: 10}, false},
	}
	for _, tc := range cases {
		err := validateDemoWalletTransfer(tc.op)
		if (err == nil) != tc.good {
			t.Errorf("validateDemoWalletTransfer(%+v) error=%v, want valid=%v", tc.op, err, tc.good)
		}
	}
}

func TestDemoWalletTransferAPI(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	token, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateSession(context.Background(), userID, hashToken(token)); err != nil {
		t.Fatal(err)
	}

	h := &Handler{s: &Service{repo: repo, demoCredits: true}}
	endpoint := h.s.RequireAuth(http.HandlerFunc(h.DemoWalletTransfer))
	req := func(method, body string, authorized bool) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/api/wallet/demo-transfer", strings.NewReader(body))
		if authorized {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		endpoint.ServeHTTP(w, request)
		return w
	}

	if got := req(http.MethodPost, `{"kind":"deposit","amount":100}`, false); got.Code != 401 {
		t.Fatalf("anonymous status=%d, want 401", got.Code)
	}
	if got := req(http.MethodGet, "", true); got.Code != 405 {
		t.Fatalf("GET transfer status=%d, want 405", got.Code)
	}

	disabled := &Handler{s: &Service{repo: repo, demoCredits: false}}
	production := disabled.s.RequireAuth(http.HandlerFunc(disabled.DemoWalletTransfer))
	productionRequest := httptest.NewRequest(http.MethodPost, "/api/wallet/demo-transfer",
		strings.NewReader(`{"kind":"deposit","amount":100}`))
	productionRequest.Header.Set("Authorization", "Bearer "+token)
	disabledRecorder := httptest.NewRecorder()
	production.ServeHTTP(disabledRecorder, productionRequest)
	if disabledRecorder.Code != 403 {
		t.Fatalf("production demo transfer status=%d, want 403", disabledRecorder.Code)
	}

	for _, bad := range []string{
		`{"kind":"deposit","amount":0}`,
		`{"kind":"withdraw","amount":-1}`,
		`{"kind":"deposit","amount":100001}`,
		`{"kind":"deposit","amount":1.5}`,
		`{"kind":"payout","amount":10}`,
		`{"kind":"deposit","amount":100,"real_money":true}`,
		`{"kind":"deposit","amount":10} {}`,
		`not json`,
	} {
		rec := req(http.MethodPost, bad, true)
		if rec.Code != 400 {
			t.Fatalf("bad payload %q: code=%d, body=%s", bad, rec.Code, rec.Body.String())
		}
	}

	deposit := req(http.MethodPost, `{"kind":"deposit","amount":1000}`, true)
	if deposit.Code != 200 {
		t.Fatalf("deposit: code=%d, body=%s", deposit.Code, deposit.Body.String())
	}
	var state struct {
		Balance      int64  `json:"balance"`
		Amount       int64  `json:"amount"`
		Currency     string `json:"currency"`
		RealTransfer bool   `json:"real_transfer"`
	}
	if err := json.Unmarshal(deposit.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Balance != 1000 || state.Amount != 1000 ||
		state.Currency != "virtual_coins" || state.RealTransfer {
		t.Fatalf("deposit response unexpectedly represents real funds: %+v", state)
	}

	withdraw := req(http.MethodPost, `{"kind":"withdraw","amount":250}`, true)
	if withdraw.Code != 200 {
		t.Fatalf("withdraw: code=%d, body=%s", withdraw.Code, withdraw.Body.String())
	}
	tooMuch := req(http.MethodPost, `{"kind":"withdraw","amount":751}`, true)
	if tooMuch.Code != 409 {
		t.Fatalf("overdraft code=%d, body=%s", tooMuch.Code, tooMuch.Body.String())
	}

	balance, txs, err := repo.Wallet(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 750 || len(txs) != 2 {
		t.Fatalf("wallet balance=%d txs=%d; want 750 and 2", balance, len(txs))
	}
	if txs[0].Amount != -250 || txs[0].Type != "demo-withdraw" ||
		txs[1].Amount != 1000 || txs[1].Type != "demo-deposit" {
		t.Fatalf("wrong transaction ledger: %+v", txs)
	}
}

func TestConcurrentDemoWalletWithdrawalsDoNotOverdraw(t *testing.T) {
	repo, userID := numberGameTestRepository(t)
	ctx := context.Background()
	_, err := repo.ChangeBalance(ctx, userID, 1000, "test-seed", "Initial test coins")
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := repo.ChangeBalance(ctx, userID, -700, "demo-withdraw", "Test withdrawal")
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	success, insufficient := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if err == errInsufficientBalance {
			insufficient++
		} else {
			t.Fatalf("unexpected concurrent withdrawal error: %v", err)
		}
	}
	if success != 1 || insufficient != 1 {
		t.Fatalf("expected one successful withdrawal and one rejected, got %d and %d", success, insufficient)
	}
	balance, operations, err := repo.Wallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if balance != 300 || len(operations) != 2 {
		t.Fatalf("balance=%d, operations=%d, want 300 and 2", balance, len(operations))
	}
}
