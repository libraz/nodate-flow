package admin

import "testing"

func TestValidateSettingValue_MFAEnforcementThreeState(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"none", "optional", "required"} {
		if err := validateSettingValue("mfa_enforcement", value); err != nil {
			t.Fatalf("mfa_enforcement=%q should be accepted: %v", value, err)
		}
	}
	for _, value := range []string{"true", "false", "", "enforced"} {
		if err := validateSettingValue("mfa_enforcement", value); err == nil {
			t.Fatalf("mfa_enforcement=%q should be rejected", value)
		}
	}
}

func TestValidateSettingValue_RegistrationOpenBoolean(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"true", "false"} {
		if err := validateSettingValue("registration_open", value); err != nil {
			t.Fatalf("registration_open=%q should be accepted: %v", value, err)
		}
	}
	for _, value := range []string{"none", "optional", "required", ""} {
		if err := validateSettingValue("registration_open", value); err == nil {
			t.Fatalf("registration_open=%q should be rejected", value)
		}
	}
}
