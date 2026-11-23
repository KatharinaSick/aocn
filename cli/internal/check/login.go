package check

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/katharinasick/aocn/cli/internal/kubectl"
)

// Login describes a login check: a workload logs in at an OpenID Connect issuer as a client,
// and then, if Target is set, sends a request with the access token.
type Login struct {
	// Issuer is the issuer's address in the cluster, written like a target.
	Issuer Target
	Client string
	// Secret is the client's secret. Without one, the workload logs in with its
	// ServiceAccount token as a client assertion (RFC 7523).
	Secret string
	From   Source
	// Method and Target are the request sent after the login. Without a Target, the check
	// only logs in.
	Method string
	Target *Target
	Expect Expect
}

// tokenDir is where a login check's test pod gets its ServiceAccount token for the issuer.
const tokenDir = "/var/run/secrets/aocn"

// Describe says what the check does, like the other checks: who logs in as whom, and where
// it sends the request.
func (l Login) Describe() string {
	who := l.From.Describe()
	if l.From.Kind == KindDeployment && l.Secret == "" {
		who += "'s ServiceAccount"
	}
	if l.Target == nil {
		what := fmt.Sprintf("%s logging in as %s", who, l.Client)
		if l.Secret != "" {
			what += " with a secret"
		}
		return what
	}
	as := l.Client
	if l.Secret != "" {
		as += " with a secret"
	}
	return fmt.Sprintf("%s as %s → %s %s %s", who, as, l.Target.Service, l.Method, l.Target.Path)
}

// Matches reports whether the probe's answer is the expected one. A refused login (the probe
// answers "login: 401 Unauthorized", or any other 4xx) counts as blocked, and fails anything
// else. A login that didn't get through to the issuer ("login: error: ...") fails every
// expectation: it says nothing about what the workload may do. Everything else, the answer
// to the request after the login, is matched like an HTTP check's.
func (l Login) Matches(answer string) bool {
	rest, ok := strings.CutPrefix(answer, "login: ")
	if !ok {
		return l.Expect.Matches(answer)
	}
	status, _, _ := strings.Cut(rest, " ")
	code, err := strconv.Atoi(status)
	return err == nil && code/100 == 4 && l.Expect.Blocked
}

// RunLogin runs a login check from a test pod. The error is only for Ctrl+C: everything else
// is a failed check.
//
// It always runs in a new pod, never with kubectl exec, because where a workload mounts its
// token is up to the player. For a Deployment or a CronJob, the pod gets the workload's
// ServiceAccount and labels, so it logs in as the workload would. A new pod gets the
// namespace's default ServiceAccount. Without a secret, the pod gets a ServiceAccount token
// for the issuer, which is what the workload would need to mount itself.
func RunLogin(ctx context.Context, f *Fixtures, l Login) (Result, error) {
	pass, fail := l.Expect.Describe(l.Describe())
	answer, err := loginProbe(ctx, f, l)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		answer = err.Error()
	}
	r := Result{Passed: err == nil && l.Matches(answer), Got: answer}
	if r.Passed {
		return said(r, pass), nil
	}
	return said(r, fmt.Sprintf("%s, but got: %s", fail, answer)), nil
}

func loginProbe(ctx context.Context, f *Fixtures, l Login) (string, error) {
	issuer := l.Issuer.URL()
	args := []string{"/station", "--role", "probe", "--login-issuer", issuer, "--login-client", l.Client}
	if l.Secret != "" {
		args = append(args, "--login-secret", l.Secret)
	} else {
		args = append(args, "--login-assertion-file", tokenDir+"/token")
	}
	if l.Target != nil {
		args = append(args, "--method", l.Method, "--url", l.Target.URL())
	}

	account, labels := "default", "app="+fixtureName
	switch l.From.Kind {
	case KindDeployment, KindCronJob:
		var err error
		account, labels, err = podIdentity(ctx, l.From)
		if err != nil {
			return "", err
		}
	case KindNewPod:
		// Only the destination decides, as for an HTTP check from a new pod.
		if err := f.egress(ctx, l.From.Namespace); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unknown workload kind %q", l.From.Kind)
	}
	overrides, err := loginOverrides(account, issuer, l.Secret == "")
	if err != nil {
		return "", err
	}
	return probeFromTestPod(ctx, l.From.Namespace, labels, args, overrides...)
}

// podIdentity returns the ServiceAccount and labels (k=v,k=v) of a Deployment's or CronJob's
// pods. Pods without a ServiceAccount of their own run as default. The deprecated
// serviceAccount counts too: kubectl apply leaves it behind when serviceAccountName is removed
// from a file, and the pods keep running as that account. A Deployment's pod template has no
// pod-template-hash, so its ReplicaSet doesn't take the test pod over.
func podIdentity(ctx context.Context, s Source) (account, labels string, err error) {
	spec := ".spec.template"
	kind := "deployment"
	if s.Kind == KindCronJob {
		spec, kind = ".spec.jobTemplate.spec.template", "cronjob"
	}
	out, err := kubectl.Run(ctx, "get", kind, "-n", s.Namespace, s.Name, "-o",
		"go-template={{with "+spec+".spec.serviceAccountName}}{{.}}{{else}}{{with "+spec+".spec.serviceAccount}}{{.}}{{end}}{{end}}|{{range $k, $v := "+spec+".metadata.labels}}{{$k}}={{$v}},{{end}}")
	if err != nil {
		return "", "", err
	}
	account, labels, _ = strings.Cut(strings.TrimSpace(out), "|")
	if account == "" {
		account = "default"
	}
	return account, strings.TrimSuffix(labels, ","), nil
}

// loginOverrides is what a login check's test pod gets on top of kubectl run's: the
// ServiceAccount, and with withToken a ServiceAccount token for the issuer at tokenDir. It's
// a strategic merge, so the container is matched by its name, which kubectl run sets to the
// pod's: probeFromTestPod fills it in.
func loginOverrides(account, audience string, withToken bool) ([]string, error) {
	spec := map[string]any{"serviceAccountName": account}
	if withToken {
		spec["containers"] = []map[string]any{{
			"name":         podNamePlaceholder,
			"volumeMounts": []map[string]any{{"name": "aocn-token", "mountPath": tokenDir, "readOnly": true}},
		}}
		spec["volumes"] = []map[string]any{{
			"name": "aocn-token",
			"projected": map[string]any{"sources": []map[string]any{{
				"serviceAccountToken": map[string]any{"audience": audience, "expirationSeconds": 600, "path": "token"},
			}}},
		}}
	}
	data, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		return nil, err
	}
	return []string{"--override-type=strategic", "--overrides=" + string(data)}, nil
}

// podNamePlaceholder stands for the test pod's name in overrides, which is only known when
// the pod is created.
const podNamePlaceholder = "{{pod}}"
