package main

import "testing"

func TestMFARequiredRoles(t *testing.T) {
	cases := map[string]bool{
		"chef":           true,
		"platform_admin": true,
		"employee":       false,
		"":               false,
	}
	for role, want := range cases {
		if got := mfaRequired(role); got != want {
			t.Errorf("mfaRequired(%q) = %v, want %v", role, got, want)
		}
	}
}

func TestMFAAllowedWithoutEnrollment(t *testing.T) {
	allowed := []string{"/api/me", "/api/logout", "/api/mfa/enroll", "/api/mfa/confirm"}
	for _, path := range allowed {
		if !mfaAllowedWithoutEnrollment(path) {
			t.Errorf("mfaAllowedWithoutEnrollment(%q) = false, want true", path)
		}
	}
	denied := []string{"/api/users", "/api/patients", "/api/mfa/disable", "/api/mfa/recovery-codes"}
	for _, path := range denied {
		if mfaAllowedWithoutEnrollment(path) {
			t.Errorf("mfaAllowedWithoutEnrollment(%q) = true, want false", path)
		}
	}
}
