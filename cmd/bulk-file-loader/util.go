package cli

type partialError struct{}

func (partialError) Error() string { return "partial failure" }
