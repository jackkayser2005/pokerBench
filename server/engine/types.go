package engine

type Seat string

const (
	SB Seat = "SB"
	BB Seat = "BB"
)

type ActionKind string

const (
	Fold  ActionKind = "fold"
	Check ActionKind = "check"
	Call  ActionKind = "call"
	Raise ActionKind = "raise"
)

type Action struct {
	Seat   Seat       `json:"seat"`
	Kind   ActionKind `json:"action"`
	Amount int        `json:"to,omitempty"`
}

type Card struct {
	Rank int
	Suit byte
} // e.g. "As" => rank 14, suit 's'

// Observation struct will come when we hook models; engine itself doesn’t need it yet.
