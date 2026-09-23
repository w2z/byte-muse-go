package domain

// Actor is a legacy performer record. A non-null limit_date means the actor is subscribed.
type Actor struct {
	Name      string  `json:"name"`
	Photo     *string `json:"photo"`
	LimitDate *string `json:"limit_date"`
}
