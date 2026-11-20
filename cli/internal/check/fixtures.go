package check

import (
	"context"
	"sync"

	"github.com/katharinasick/aocn/cli/internal/kubectl"
)

// Fixtures are what checks share during one run: the test server, and the policy that opens
// test pods' egress, once per namespace. Checks run at the same time, so each is created
// once, by whichever check needs it first, and deleted by Close after all of them are done.
// One check deleting a fixture another one still uses would change what that one finds.
type Fixtures struct {
	mu       sync.Mutex
	server   *once[string]
	egresses map[string]*once[struct{}]
	cleanups []func(context.Context)
}

// once is a fixture that's created the first time it's needed.
type once[T any] struct {
	do  sync.Once
	v   T
	err error
}

// testServer returns the test server's namespace, and starts it the first time.
func (f *Fixtures) testServer(ctx context.Context) (string, error) {
	f.mu.Lock()
	if f.server == nil {
		f.server = &once[string]{}
	}
	o := f.server
	f.mu.Unlock()
	o.do.Do(func() {
		o.v, o.err = startTestServer(ctx)
		if o.v != "" {
			namespace := o.v
			f.onClose(func(ctx context.Context) {
				kubectl.Run(ctx, "delete", "namespace", namespace, "--wait=false")
			})
		}
	})
	return o.v, o.err
}

// egress opens test pods' egress in namespace, the first time it's asked to.
func (f *Fixtures) egress(ctx context.Context, namespace string) error {
	f.mu.Lock()
	if f.egresses == nil {
		f.egresses = map[string]*once[struct{}]{}
	}
	o, ok := f.egresses[namespace]
	if !ok {
		o = &once[struct{}]{}
		f.egresses[namespace] = o
	}
	f.mu.Unlock()
	o.do.Do(func() {
		// Deleted even if applying it failed halfway.
		f.onClose(func(ctx context.Context) {
			kubectl.Run(ctx, "delete", "networkpolicy", "-n", namespace, fixtureName, "--ignore-not-found")
		})
		o.err = applyEgressFixture(ctx, namespace)
	})
	return o.err
}

func (f *Fixtures) onClose(fn func(context.Context)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanups = append(f.cleanups, fn)
}

// Close deletes every fixture, also after Ctrl+C.
func (f *Fixtures) Close() {
	f.mu.Lock()
	fns := f.cleanups
	f.cleanups = nil
	f.mu.Unlock()
	for _, fn := range fns {
		cleanup(fn)
	}
}
