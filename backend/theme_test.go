package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// These checks guard the frontend theme without introducing a JS build system.
func TestFrontendAuroraThemeAndReducedMotion(t *testing.T) {
	bytes, err := os.ReadFile("../index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(bytes)

	for _, required := range []string{
		`--gradient: linear-gradient(`,
		`--bg: #090810`,
		`body::before`,
		`body::after`,
		`.brand:hover .mark`,
		`.tabs button.active`,
		`.card:hover`,
		`@keyframes auroraFloat`,
		`@keyframes pageEnter`,
		`@keyframes cardRise`,
		`@keyframes balancePulse`,
		`@media (max-width: 420px)`,
		`@media (prefers-reduced-motion: reduce)`,
		`animation: none !important;`,
		`button:focus-visible`,
		`setWalletBalance(balance)`,
		`window.matchMedia("(prefers-reduced-motion: reduce)")`,
	} {
		if !strings.Contains(page, required) {
			t.Errorf("missing theme/accessibility feature: %s", required)
		}
	}

	// Keep existing authenticated workflows and navigation names intact.
	for _, route := range []string{
		`"/api/login"`,
		`"/api/profile"`,
		`"/api/wallet"`,
		`"/api/game/guess"`,
		`"/api/referrals"`,
	} {
		if !strings.Contains(page, route) {
			t.Errorf("the redesign removed API route %s", route)
		}
	}
	tabs := regexp.MustCompile(`data-page="([^"]+)"`).FindAllStringSubmatch(page, -1)
	var names []string
	for _, tab := range tabs {
		names = append(names, tab[1])
	}
	if got := strings.Join(names, ","); got != "games,profile,stats,referrals" {
		t.Fatalf("navigation was changed: %s", got)
	}

	for _, element := range []string{
		"walletHeaderBalance", "openWalletDeposit", "openWalletWithdraw",
		"openProfileSettings", "walletHistory", "profileStats", "referralLink",
		"numberGameForm", "profileSettingsForm", "claimDailyXP",
	} {
		if !strings.Contains(page, `id="`+element+`"`) {
			t.Errorf("redesign removed required element %q", element)
		}
	}
}
