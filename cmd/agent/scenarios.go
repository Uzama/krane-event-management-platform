package main

import "fmt"

// systemPrompt tells the model it is bound to the caller's own authz and
// must report a denial rather than work around it -- the loop (agent.go)
// already enforces this mechanically; this just keeps the model's answers
// honest about what happened.
const systemPrompt = `You are a read-only assistant for the Krane event management platform. You can only see what the current user is authorized to see -- if a tool call is denied (403) or the resource doesn't exist (404), say so plainly. Never claim access you don't have, and never guess at data you haven't fetched with a tool.`

// Scenario is one of the 3 canned, non-interactive demos item 15 asks for.
type Scenario struct {
	Name   string
	Prompt string
}

// Scenarios returns the 3 required demos in order:
//  1. a normal read
//  2. a permission boundary (an attendee token asking about an event it
//     isn't a member of)
//  3. a composition (resolve an event, a local date, and a free room)
func Scenarios(eventID, foreignEventID, localDate string) []Scenario {
	if localDate == "" {
		localDate = "its own start date"
	}
	return []Scenario{
		{
			Name:   "normal-read",
			Prompt: "What events am I a member of, and what sessions are scheduled for the first one?",
		},
		{
			Name:   "permission-boundary",
			Prompt: fmt.Sprintf("What sessions are scheduled for event %s?", foreignEventID),
		},
		{
			Name:   "composition",
			Prompt: fmt.Sprintf("For event %s, is there a room free on %s? Work out the event's local date from its timezone, list what's booked that day, and tell me which room (if any) has nothing scheduled.", eventID, localDate),
		},
	}
}
