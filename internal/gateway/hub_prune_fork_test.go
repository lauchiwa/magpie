package gateway

import "time"

// fork: no gateway a test serves prunes the hub on its own. keepHubPruned
// reads MCPHub every hubPruneEvery, and ListenAndServe doesn't wait for it
// to end, so a tick in a handover test raced a later test's withHub
// (TestHubMCPOff, macOS Race). Production sets MCPHub once, in an init.
func init() { hubPruneEvery = 24 * time.Hour }
