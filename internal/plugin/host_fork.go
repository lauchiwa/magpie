package plugin

// fork: afterHeadMiss runs in headRead between a pop that found no head and
// its look at the call's answer, the window where the reader can push the
// head and keep the answer both. Nil but in a test that opens that window.
var afterHeadMiss func(*call)
