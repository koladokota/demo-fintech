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
	if want := []string{"games", "profile", "stats", "referrals"}; !reflect.DeepEqual(tabs, want) {
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

	// Wallet, statistics and settings occupy independent sections.
	profileStart := strings.Index(html, `id="page-profile"`)
	statsStart := strings.Index(html, `id="page-stats"`)
	settingsStart := strings.Index(html, `id="page-settings"`)
	depositStart := strings.Index(html, `id="page-deposit"`)
	withdrawStart := strings.Index(html, `id="page-withdraw"`)
	referralsStart := strings.Index(html, `id="page-referrals"`)
	if profileStart < 0 || statsStart <= profileStart || settingsStart <= statsStart ||
		depositStart <= settingsStart || withdrawStart <= depositStart || referralsStart <= withdrawStart {
		t.Fatal("wallet, statistics and settings screens are not separate")
	}
	profile := html[profileStart:statsStart]
	stats := html[statsStart:settingsStart]
	settings := html[settingsStart:depositStart]
	for _, expected := range []string{
		`id="profileAvatar"`, `id="profileName"`, `id="walletBalance"`,
		`id="walletHistory"`, `class="profile-layout"`, `class="card activity-card"`,
		`id="openProfileSettings"`,
	} {
		if !strings.Contains(profile, expected) {
			t.Errorf("profile missing expected element %q", expected)
		}
	}
	for _, forbidden := range []string{"walletDepositForm", "walletWithdrawForm", "walletDepositPanel",
		"walletWithdrawPanel", "profileSettingsForm", "profileStats", "profileAchievements",
		"playerXP", "claimDailyXP", `<h1>Профиль</h1>`} {
		if strings.Contains(profile, forbidden) {
			t.Errorf("profile still contains unwanted element %q", forbidden)
		}
	}
	for _, expected := range []string{`id="profileStats"`, `id="playerLevel"`, `id="claimDailyXP"`} {
		if !strings.Contains(stats, expected) {
			t.Errorf("stats page is missing %q", expected)
		}
	}
	for _, expected := range []string{`id="profileSettingsForm"`, `id="avatarChoices"`, `id="profileNameInput"`} {
		if !strings.Contains(settings, expected) {
			t.Errorf("settings page missing %q", expected)
		}
	}
	if strings.Contains(html, `id="profileAchievements"`) {
		t.Error("achievements should not be visible in the minimalist UI")
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
