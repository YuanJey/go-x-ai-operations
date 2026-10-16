package engagement

import (
	"fmt"
	"regexp"
	"strings"
)

func CheckEligibility(input EligibilityInput) Eligibility {
	handle := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(input.AccountHandle)), "@")
	if handle == "" {
		return Eligibility{Reason: "account handle is missing"}
	}
	pattern := regexp.MustCompile(`(?i)(?:^|[^\w])@` + regexp.QuoteMeta(handle) + `(?:$|[^\w])`)
	if pattern.MatchString(input.Text) {
		return Eligibility{
			Eligible: true,
			Evidence: fmt.Sprintf("original author explicitly mentioned @%s", handle),
		}
	}
	for _, postID := range input.ReferencedPostIDs {
		if input.AccountPostIDs[postID] {
			return Eligibility{
				Eligible: true,
				Evidence: "original author referenced an account-owned post " + postID,
			}
		}
	}
	return Eligibility{
		Reason: "original author did not explicitly mention the account or reference an account-owned post",
	}
}

func ValidateReplyParticipants(
	replyBody string,
	allowedHandles map[string]bool,
) error {
	mentionPattern := regexp.MustCompile(`(?i)@[a-z0-9_]{1,15}`)
	for _, value := range mentionPattern.FindAllString(replyBody, -1) {
		handle := strings.TrimPrefix(strings.ToLower(value), "@")
		if !allowedHandles[handle] {
			return fmt.Errorf("reply introduces thread-external participant @%s", handle)
		}
	}
	return nil
}
