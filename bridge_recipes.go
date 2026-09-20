package workgraph

import (
	"errors"
	"fmt"
	"time"
)

// MicrosoftMailRecipeBounds is the mailbox-local provider query window for one
// exact UTC capture request.
type MicrosoftMailRecipeBounds struct {
	Since time.Time
	Until time.Time
}

// MicrosoftMailRecipeWindow returns a two-calendar-day padded window in the
// mailbox timezone. Results still require exact UTC filtering after fetching.
func MicrosoftMailRecipeWindow(since, until time.Time, location *time.Location) (MicrosoftMailRecipeBounds, error) {
	if location == nil {
		return MicrosoftMailRecipeBounds{}, errors.New("Microsoft Mail mailbox timezone is required")
	}
	if !since.Before(until) {
		return MicrosoftMailRecipeBounds{}, errors.New("Microsoft Mail request bounds must be increasing")
	}
	localSince := since.In(location)
	localUntil := until.In(location)
	return MicrosoftMailRecipeBounds{
		Since: localCalendarShift(localSince, -2),
		Until: localCalendarShift(localUntil, 2),
	}, nil
}

func localCalendarShift(value time.Time, days int) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location()).AddDate(0, 0, days)
}

// MicrosoftMailRecipeMessage is the sanitized provider-shaped subset needed
// to prove bounded mail pagination and normalization.
type MicrosoftMailRecipeMessage struct {
	ID               string
	ReceivedDateTime string
}

// MicrosoftMailRecipePage is one ordered provider response page. URL and
// NextURL make offset pagination continuity explicit in executable facts.
type MicrosoftMailRecipePage struct {
	URL       string
	NextURL   string
	Messages  []MicrosoftMailRecipeMessage
	Complete  bool
	Truncated bool
}

// NormalizeMicrosoftMailRecipe follows contiguous pages and filters provider
// results against the exact UTC request bounds.
func NormalizeMicrosoftMailRecipe(pages []MicrosoftMailRecipePage, since, until time.Time) ([]MicrosoftMailRecipeMessage, error) {
	if len(pages) == 0 {
		return nil, errors.New("Microsoft Mail recipe returned no pages")
	}
	if !since.Before(until) {
		return nil, errors.New("Microsoft Mail request bounds must be increasing")
	}

	messages := make([]MicrosoftMailRecipeMessage, 0)
	var expectedURL string
	var previous time.Time
	crossedLowerBound := false
	for index, page := range pages {
		if page.URL == "" {
			return nil, fmt.Errorf("Microsoft Mail recipe page %d has no URL", index+1)
		}
		if index == 0 {
			expectedURL = page.URL
		}
		if page.URL != expectedURL {
			return nil, fmt.Errorf("Microsoft Mail recipe pagination jumped to %q, expected %q", page.URL, expectedURL)
		}
		if page.Truncated {
			return nil, fmt.Errorf("Microsoft Mail recipe page %q is truncated", page.URL)
		}

		for _, message := range page.Messages {
			if message.ID == "" {
				return nil, errors.New("Microsoft Mail recipe message id is required")
			}
			received, err := time.Parse(time.RFC3339Nano, message.ReceivedDateTime)
			if err != nil {
				return nil, fmt.Errorf("parse Microsoft Mail message %q timestamp: %w", message.ID, err)
			}
			if !previous.IsZero() && received.After(previous) {
				return nil, fmt.Errorf("Microsoft Mail recipe page %q is not sorted by receivedDateTime", page.URL)
			}
			previous = received
			if received.Before(since) {
				crossedLowerBound = true
				continue
			}
			if received.Before(until) {
				messages = append(messages, message)
			}
		}

		if crossedLowerBound {
			return messages, nil
		}
		if page.NextURL == "" {
			if !page.Complete {
				return nil, fmt.Errorf("Microsoft Mail recipe page %q ended without an exhaustive completion marker", page.URL)
			}
			return messages, nil
		}
		expectedURL = page.NextURL
	}
	return nil, errors.New("Microsoft Mail recipe pagination ended before completion")
}

// BridgedEmptyProof records why a bounded provider query may safely return no
// events. A control query is an explicit provider-specific exhaustion proof.
type BridgedEmptyProof struct {
	ExhaustiveQuery    bool `json:"exhaustive_query"`
	ControlQueryPassed bool `json:"control_query_passed"`
}

// ValidateBridgedEmptyProof rejects empty bounded captures without a provider
// proof that the requested scope was fully examined.
func ValidateBridgedEmptyProof(proof BridgedEmptyProof) error {
	if proof.ExhaustiveQuery || proof.ControlQueryPassed {
		return nil
	}
	return errors.New("empty bridged capture requires an exhaustive query or passed control query")
}
