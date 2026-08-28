package run

// Exit codes. They separate "some removals failed" from "I could not even read
// the state", because those call for different responses.
const (
	// ExitOK covers a clean apply, a dry run, nothing to do, and a decline.
	ExitOK = 0
	// ExitApplyFailed means one or more removals were rejected.
	ExitApplyFailed = 1
	// ExitUsage means the invocation itself was wrong.
	ExitUsage = 2
	// ExitEnvironment means docker could not be read, so no plan was trusted.
	ExitEnvironment = 3
)
