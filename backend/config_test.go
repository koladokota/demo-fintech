package main

import "testing"

func TestDemoCreditsOnlyDevelopmentOrTest(t *testing.T) {
	t.Setenv("ENABLE_DEMO_CREDITS", "true")
	for _, env := range []string{"", "production", "staging"} {
		t.Setenv("APP_ENV", env)
		if loadConfig().DemoCredits {
			t.Fatalf("demo credits enabled for APP_ENV=%q", env)
		}
	}
	for _, env := range []string{"development", "test"} {
		t.Setenv("APP_ENV", env)
		if !loadConfig().DemoCredits {
			t.Fatalf("demo credits disabled for APP_ENV=%q", env)
		}
	}
}
