package main

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Keep wallet controls separate from the main navigation and profile body.
func TestWalletPagesAreIndependentOfProfile(t *testing.T) {
	body, err := os.ReadFile("../index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)

	navStart := strings.Index(html, "<nav class=\"tabs\"")
	navEnd := strings.Index(html[navStart:], "</nav>")
	if navStart < 0 || navEnd < 0 {
		t.Fatal("main navigation not found")
	}
	nav := html[navStart : navStart+navEnd]
	tabPattern := regexp.MustCompile(`data-page="([^"]+)"`)
	matches := tabPattern.FindAllStringSubmatch(nav, -1)
	var tabs []string
	for _, match := range matches {
		tabs = append(tabs, match[1])
	}
	if want := []string{"games", "profile", "referrals"}; !reflect.DeepEqual(tabs, want) {
		t.Fatalf("main navigation = %v; want %v", tabs, want)
	}

	for _, id := range []string{
		"openWalletDeposit", "openWalletWithdraw", "page-deposit", "page-withdraw",
		"backFromDeposit", "backFromWithdraw", "walletDepositForm", "walletWithdrawForm",
		"walletDepositBalance", "walletWithdrawBalance",
	} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing wallet element %q", id)
		}
	}

	// Inline wallet forms must not be nested within the profile section.
	profileStart := strings.Index(html, `id="page-profile"`)
	depositStart := strings.Index(html, `id="page-deposit"`)
	withdrawStart := strings.Index(html, `id="page-withdraw"`)
	referralsStart := strings.Index(html, `id="page-referrals"`)
	if profileStart < 0 || depositStart <= profileStart || withdrawStart <= depositStart || referralsStart <= withdrawStart {
		t.Fatal("wallet screens not separated into their own sections")
	}
	profile := html[profileStart:depositStart]
	if !strings.Contains(profile, `id="walletBalance"`) {
		t.Fatal("profile balance was removed")
	}
	for _, forbidden := range []string{"walletDepositForm", "walletWithdrawForm", "walletDepositPanel", "walletWithdrawPanel"} {
		if strings.Contains(profile, forbidden) {
			t.Errorf("profile still includes inline wallet form %q", forbidden)
		}
	}

	ids := regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(html, -1)
	seen := make(map[string]bool)
	for _, m := range ids {
		if seen[m[1]] {
			t.Errorf("duplicate element id %s", m[1])
		}
		seen[m[1]] = true
	}
}
