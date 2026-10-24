package plugin

import (
	"context"
	"testing"
)

// The reader pushes a call's head and then keeps its answer. A head read
// whose pop came just before both found the answer and took the reply for
// none: "the plugin answered no response", a 502 on a request the plugin
// served (TestGrokAccountDraws on ubuntu CI under -race). The head it pushed
// is read with the answer.
func TestHeadPushedBeforeTheAnswerIsRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		why  string
	}{
		{"answered", ""},
		{"failed", "the vendor exploded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHost(t)
			_, c := f.begin(t, false)
			t.Cleanup(func() { afterHeadMiss = nil })
			afterHeadMiss = func(c *call) {
				afterHeadMiss = nil
				// as the reader does: the head first, then the answer kept
				c.mu.Lock()
				c.queue = append(c.queue, message{Status: 200})
				a := message{}
				if tc.why != "" {
					a = errAnswer(tc.why)
				}
				c.terminal = &a
				c.mu.Unlock()
			}
			head, answer, err := c.headRead(context.Background())
			if err != nil || head.Status != 200 {
				t.Fatalf("head = %+v, %v: a head pushed before the answer was lost", head, err)
			}
			if answer == nil {
				t.Fatal("the answer was lost: the body would never end")
			}
			if (answer.Error != nil) != (tc.why != "") {
				t.Fatalf("answer = %+v, want error %q", answer, tc.why)
			}
		})
	}
}
