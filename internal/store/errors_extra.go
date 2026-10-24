package store

// UnknownAccountError names the account a posting tried to use.
type UnknownAccountError struct {
	ID string
}

func (e *UnknownAccountError) Error() string { return "unknown account " + e.ID }

// Unwrap lets errors.Is match ErrUnknownAccount.
func (e *UnknownAccountError) Unwrap() error { return ErrUnknownAccount }
