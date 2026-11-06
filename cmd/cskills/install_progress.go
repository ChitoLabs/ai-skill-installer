package main

// installEvent reports an active installer phase. Atomic becomes
// true immediately before preparation hands control to the transaction; it
// remains true for every later event.
type installEvent struct {
	Phase  string
	Atomic bool
}
