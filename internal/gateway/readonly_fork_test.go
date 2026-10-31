package gateway

// fork: the identity capture routes (identity.go) are refused a read-only
// key, as every route not in readOnlyRoutes is: arming and disarming are
// writes, and what is armed is no stat of the quotas or usage it reads.
// TestReadOnlyKeyEveryRoute asks each route to be classified.
func init() {
	readOnlyRefused = append(readOnlyRefused,
		"POST "+IdentityArmPath, "POST "+IdentityDisarmPath, "GET "+IdentityArmedPath)
}
