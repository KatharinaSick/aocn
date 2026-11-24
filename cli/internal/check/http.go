package check

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/katharinasick/aocn/cli/internal/kubectl"
)

// HTTP sends a request from source to target and passes if the answer matches expect. The
// error is only for Ctrl+C: everything else is a failed check.
//
// Requests are sent with the station binary's probe role: inside the Deployment's pod (same
// identity as the workload), or from a short-lived test pod. Test pods are deleted before
// HTTP returns, also when ctx is cancelled. The test server and the egress fixture come from
// f, which deletes them once all checks are done.
func HTTP(ctx context.Context, f *Fixtures, method string, source Source, target Target, data string, expect Expect) (Result, error) {
	to := target.Service
	if target.TestServer {
		to = "a test server"
	}
	what := fmt.Sprintf("%s → %s %s %s", source.Describe(), to, method, target.Path)
	pass, fail := expect.Describe(what)

	answer, err := func() (string, error) {
		if target.TestServer {
			namespace, err := f.testServer(ctx)
			if err != nil {
				return "", err
			}
			target.Namespace = namespace
		}
		return probe(ctx, f, source, target, method, data)
	}()
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		answer = err.Error()
	}
	r := Result{Passed: err == nil && expect.Matches(answer), Got: answer}
	if r.Passed {
		return said(r, pass), nil
	}
	return said(r, fmt.Sprintf("%s, but got: %s", fail, answer)), nil
}

// probe returns the probe's answer: the status ("200 OK"), or "error: ..." if the request
// didn't get through. An error means the probe itself couldn't run.
func probe(ctx context.Context, f *Fixtures, source Source, target Target, method, data string) (string, error) {
	probeArgs := []string{"/station", "--role", "probe", "--method", method, "--url", target.URL(), "--data", data}

	switch source.Kind {
	case KindDeployment:
		out, err := kubectl.Run(ctx, append([]string{"exec", "-n", source.Namespace, "deploy/" + source.Name, "--"}, probeArgs...)...)
		return lastLine(out), err

	case KindNewPod:
		// To the test server, the namespace's own policies are what's tested.
		if target.TestServer {
			return probeFromTestPod(ctx, source.Namespace, "app="+fixtureName+"-plain", probeArgs, testPod{})
		}
		// Elsewhere only the destination decides: the fixture opens the test pod's egress.
		if err := f.egress(ctx, source.Namespace); err != nil {
			return "", err
		}
		return probeFromTestPod(ctx, source.Namespace, "app="+fixtureName, probeArgs, testPod{})

	case KindCronJob:
		// The pod gets the job's labels and so the job's policies. This tests what the
		// player allowed the job.
		labels, err := kubectl.Run(ctx, "get", "cronjob", "-n", source.Namespace, source.Name,
			"-o", "go-template={{range $k, $v := .spec.jobTemplate.spec.template.metadata.labels}}{{$k}}={{$v}},{{end}}")
		if err != nil {
			return "", err
		}
		return probeFromTestPod(ctx, source.Namespace, strings.TrimSuffix(labels, ","), probeArgs, testPod{})
	}
	return "", fmt.Errorf("unknown workload kind %q", source.Kind)
}

const fixtureName = "verify-probe"

// testServerImage is the Kubernetes project's own test server. netexec answers every
// request, so anything but a 2xx means something blocked it.
const testServerImage = "registry.k8s.io/e2e-test-images/agnhost:2.53"

// startTestServer creates a namespace with no policies and a test server in it, and returns
// the namespace. Only the sender's policies decide whether a request reaches it. The caller
// deletes the namespace, also when starting the server failed.
func startTestServer(ctx context.Context) (string, error) {
	namespace := fmt.Sprintf("aocn-verify-%d", rand.IntN(100000))
	if _, err := kubectl.Run(ctx, "create", "namespace", namespace); err != nil {
		return "", err
	}
	if _, err := kubectl.Run(ctx, "run", TestServerTarget, "-n", namespace, "--image="+testServerImage,
		"--port", fmt.Sprint(defaultPort), "--", "netexec", fmt.Sprintf("--http-port=%d", defaultPort)); err != nil {
		return namespace, err
	}
	if _, err := kubectl.Run(ctx, "expose", "pod", TestServerTarget, "-n", namespace, "--port", fmt.Sprint(defaultPort)); err != nil {
		return namespace, err
	}
	if _, err := kubectl.Run(ctx, "wait", "-n", namespace, "pod/"+TestServerTarget,
		"--for=condition=Ready", "--timeout=90s"); err != nil {
		return namespace, fmt.Errorf("the test server didn't start: %w", err)
	}
	return namespace, nil
}

// applyEgressFixture lets test pods in namespace send anywhere, so only the destination's
// policies decide. It's a plain Kubernetes NetworkPolicy, so it works with any network that
// enforces them.
func applyEgressFixture(ctx context.Context, namespace string) error {
	_, err := kubectl.RunWithInput(ctx, fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  podSelector:
    matchLabels:
      app: %[1]s
  policyTypes: [Egress]
  egress:
    - {}
`, fixtureName, namespace), "apply", "-f", "-")
	return err
}

// testPods counts the test pods this aocn started, for their names.
var testPods atomic.Int64

// testPod is what a test pod gets on top of kubectl run's.
type testPod struct {
	// Account is the pod's ServiceAccount. Empty is the namespace's default.
	Account string
	// TokenAudience, if set, gives the pod a ServiceAccount token addressed to it, at
	// tokenDir/token.
	TokenAudience string
}

// probeFromTestPod runs the probe in a new pod with labels (k=v,k=v) and deletes it after.
func probeFromTestPod(ctx context.Context, namespace, labels string, probeArgs []string, opts testPod) (string, error) {
	image, err := stationImage(ctx)
	if err != nil {
		return "", err
	}
	// The counter keeps the pods of one run apart, the random part apart from an earlier run's
	// that may still be terminating.
	pod := fmt.Sprintf("%s-%d-%d", fixtureName, rand.IntN(100000), testPods.Add(1))
	args, err := testPodArgs(pod, namespace, image, labels, probeArgs, opts)
	if err != nil {
		return "", err
	}
	if _, err := kubectl.Run(ctx, args...); err != nil {
		return "", err
	}
	defer cleanup(func(ctx context.Context) {
		kubectl.Run(ctx, "delete", "pod", "-n", namespace, pod, "--wait=false")
	})

	if _, err := kubectl.Run(ctx, "wait", "-n", namespace, "pod/"+pod,
		"--for=jsonpath={.status.phase}=Succeeded", "--timeout=60s"); err != nil {
		return "", fmt.Errorf("the test pod didn't finish: %w", err)
	}
	out, err := kubectl.Run(ctx, "logs", "-n", namespace, pod)
	return lastLine(out), err
}

// testPodArgs is kubectl run's arguments for a test pod. The overrides are a strategic merge,
// so the container is matched by its name, which kubectl run sets to the pod's.
//
// The pod is never ready: it can carry a workload's labels, and a Service that selects them
// would otherwise send its traffic to the pod, which doesn't answer, while checks run at the
// same time. Nothing waits for it to be ready, only for it to finish. The station image has
// no shell, so the probe connects to a port nothing listens on.
func testPodArgs(pod, namespace, image, labels string, probeArgs []string, opts testPod) ([]string, error) {
	container := map[string]any{
		"name":           pod,
		"readinessProbe": map[string]any{"tcpSocket": map[string]any{"port": 1}, "periodSeconds": 60},
	}
	spec := map[string]any{"containers": []map[string]any{container}}
	if opts.Account != "" {
		spec["serviceAccountName"] = opts.Account
	}
	if opts.TokenAudience != "" {
		container["volumeMounts"] = []map[string]any{{"name": "aocn-token", "mountPath": tokenDir, "readOnly": true}}
		spec["volumes"] = []map[string]any{{
			"name": "aocn-token",
			"projected": map[string]any{"sources": []map[string]any{{
				"serviceAccountToken": map[string]any{"audience": opts.TokenAudience, "expirationSeconds": 600, "path": "token"},
			}}},
		}}
	}
	overrides, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		return nil, err
	}
	// The delay gives the network time to apply the pod's policies before the request.
	args := []string{"run", pod, "-n", namespace, "--image=" + image, "--restart=Never",
		"--labels=" + strings.TrimPrefix(labels+",aocn-verify=probe", ","),
		"--override-type=strategic", "--overrides=" + string(overrides),
		"--command", "--"}
	args = append(args, probeArgs...)
	return append(args, "--delay", "2s"), nil
}

// stationImage is the station image that already runs in the cluster, so test pods don't
// need another pull. AOCN_STATION_IMAGE overrides it.
func stationImage(ctx context.Context) (string, error) {
	if image := os.Getenv("AOCN_STATION_IMAGE"); image != "" {
		return image, nil
	}
	out, err := kubectl.Run(ctx, "get", "pods", "-A", "-o", "jsonpath={..image}")
	if err != nil {
		return "", err
	}
	for _, image := range strings.Fields(out) {
		if strings.Contains(image, "/aocn-station:") {
			return image, nil
		}
	}
	return "", fmt.Errorf("no station workload runs in the cluster, so there's no image for the test pod")
}

// cleanup runs fn even after ctx was cancelled, so Ctrl+C doesn't leave anything behind.
func cleanup(fn func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fn(ctx)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
