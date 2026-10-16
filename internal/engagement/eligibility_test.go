package engagement

import "testing"

func TestEligibilityRequiresExplicitSummonOrOwnedReference(t *testing.T) {
	tests := []struct {
		name     string
		input    EligibilityInput
		eligible bool
	}{
		{
			name: "explicit mention",
			input: EligibilityInput{
				AccountHandle: "@example",
				Text:          "请问 @Example 这个方案如何处理失败？",
			},
			eligible: true,
		},
		{
			name: "substring is not mention",
			input: EligibilityInput{
				AccountHandle: "@example",
				Text:          "example.com is not a summon",
			},
		},
		{
			name: "owned referenced post",
			input: EligibilityInput{
				AccountHandle:     "@example",
				ReferencedPostIDs: []string{"owned"},
				AccountPostIDs:    map[string]bool{"owned": true},
			},
			eligible: true,
		},
		{
			name: "foreign referenced post",
			input: EligibilityInput{
				AccountHandle:     "@example",
				ReferencedPostIDs: []string{"foreign"},
				AccountPostIDs:    map[string]bool{"owned": true},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := CheckEligibility(test.input)
			if result.Eligible != test.eligible {
				t.Fatalf("eligible = %t, want %t: %+v", result.Eligible, test.eligible, result)
			}
		})
	}
}

func TestReplyParticipantValidation(t *testing.T) {
	allowed := map[string]bool{"example": true, "author": true}
	if err := ValidateReplyParticipants("@author 可以这样处理", allowed); err != nil {
		t.Fatalf("allowed participant rejected: %v", err)
	}
	if err := ValidateReplyParticipants("@outsider 请加入", allowed); err == nil {
		t.Fatal("thread-external participant was accepted")
	}
}
