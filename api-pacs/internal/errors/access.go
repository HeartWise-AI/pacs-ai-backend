package errors

const (
	// RoleInUse is returned when a custom role still has members.
	RoleInUse string = "ROLE_IN_USE"
	// AccessUnavailable is returned when the effective access of a user
	// cannot be determined; permission-guarded routes fail closed.
	AccessUnavailable string = "ACCESS_UNAVAILABLE"
)
