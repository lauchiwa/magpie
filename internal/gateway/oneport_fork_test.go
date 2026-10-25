package gateway

// forkHandlerConsts names the routes the fork adds to Handler for
// TestClientPathCoversHandler. Identity capture (identity.go) lives under
// /v1/magpie/, as heap and cpu do, so `magpie web`'s port hands it to the
// gateway. identityOnlyHere still refuses any caller that isn't this
// machine.
func forkHandlerConsts(consts map[string]string) {
	consts["IdentityArmPath"] = IdentityArmPath
	consts["IdentityDisarmPath"] = IdentityDisarmPath
	consts["IdentityArmedPath"] = IdentityArmedPath
}
