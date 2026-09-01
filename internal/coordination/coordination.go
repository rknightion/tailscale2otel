// Package coordination owns Kubernetes Lease based, whole-process active-passive
// coordination. Kubernetes client-go imports intentionally stop here: the app
// layer sees only lifecycle callbacks and a small status value.
package coordination

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// ErrInvalidOptions reports a locally invalid coordinator construction request.
var ErrInvalidOptions = errors.New("invalid coordination options")

// ErrLeadershipLost is returned only after this process became leader and then
// stepped down while its caller's context was still live. Callers translate it
// to a successful process exit so Kubernetes restarts the replica.
var ErrLeadershipLost = errors.New("leadership lost")

// State is the current whole-process coordination state.
type State string

const (
	StateStandby     State = "standby"
	StateLeader      State = "leader"
	StateSteppedDown State = "stepped_down"
	StateStopped     State = "stopped"
)

// Status is safe to render in the admin status page and emit as bounded
// self-observability. Identity is deliberately pod-specific: series churn on
// failover is a chosen part of this architecture.
type Status struct {
	LeaseName string
	Namespace string
	Identity  string
	Leader    string
	State     State
}

// Options configure one Lease elector. A nil Client uses in-cluster
// authentication; tests pass a fake client explicitly.
type Options struct {
	Client        kubernetes.Interface
	LeaseName     string
	Namespace     string
	Identity      string
	LeaseDuration time.Duration
	RenewDeadline time.Duration
	RetryPeriod   time.Duration
	Logger        *slog.Logger
	Observe       func(Status)
}

// Coordinator gates an application's active lifecycle behind one Kubernetes
// Lease. It never owns application work itself.
type Coordinator struct {
	client  kubernetes.Interface
	opts    Options
	mu      sync.RWMutex
	status  Status
	observe func(Status)
}

// New builds a Lease coordinator. The in-cluster client is deliberately
// constructed only for kubernetes coordination, so mode none carries neither
// an API-server dependency nor a Kubernetes credential requirement.
func New(opts Options) (*Coordinator, error) {
	if opts.LeaseName == "" || opts.Namespace == "" ||
		opts.LeaseDuration <= opts.RenewDeadline || opts.RenewDeadline <= opts.RetryPeriod ||
		opts.RetryPeriod <= 0 {
		return nil, fmt.Errorf("%w: require non-empty lease name/namespace and lease duration > renew deadline > retry period > 0", ErrInvalidOptions)
	}
	if opts.Identity == "" {
		identity, err := os.Hostname()
		if err != nil || identity == "" {
			return nil, fmt.Errorf("%w: resolve pod identity: %w", ErrInvalidOptions, err)
		}
		opts.Identity = identity
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Client == nil {
		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("build in-cluster Kubernetes configuration: %w", err)
		}
		client, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, fmt.Errorf("build Kubernetes client: %w", err)
		}
		opts.Client = client
	}
	c := &Coordinator{
		client:  opts.Client,
		opts:    opts,
		observe: opts.Observe,
		status: Status{
			LeaseName: opts.LeaseName,
			Namespace: opts.Namespace,
			Identity:  opts.Identity,
			State:     StateStandby,
		},
	}
	c.notify(c.Status())
	return c, nil
}

// Status returns a consistent snapshot of the current state.
func (c *Coordinator) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// Run campaigns until ctx stops, the active callback returns, or this instance
// loses its lease. callback receives the election-owned context, which client-go
// cancels when renewal fails, so active work stops before Run reports demotion.
func (c *Coordinator) Run(ctx context.Context, callback func(context.Context) error) error {
	if ctx.Err() != nil {
		c.setState(StateStopped)
		return nil
	}
	if callback == nil {
		return fmt.Errorf("%w: active callback is required", ErrInvalidOptions)
	}

	var (
		activeMu   sync.Mutex
		activeErr  error
		becameLead bool
	)
	electionCtx, stopElection := context.WithCancel(ctx)
	defer stopElection()
	activeDone := make(chan struct{})
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{Name: c.opts.LeaseName, Namespace: c.opts.Namespace},
		Client:    c.client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: c.opts.Identity,
		},
	}
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: c.opts.LeaseDuration,
		RenewDeadline: c.opts.RenewDeadline,
		RetryPeriod:   c.opts.RetryPeriod,
		// Releasing on cancellation is unsafe until the callback has completed
		// its own draining. A normal demotion instead happens at RenewDeadline.
		ReleaseOnCancel: false,
		Name:            c.opts.LeaseName,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(activeCtx context.Context) {
				defer close(activeDone)
				activeMu.Lock()
				becameLead = true
				activeMu.Unlock()
				c.update(func(s *Status) {
					s.State = StateLeader
					s.Leader = c.opts.Identity
				})
				err := callback(activeCtx)
				activeMu.Lock()
				activeErr = err
				activeMu.Unlock()
				stopElection()
			},
			OnStoppedLeading: func() {
				activeMu.Lock()
				led := becameLead
				activeMu.Unlock()
				if led && ctx.Err() == nil {
					c.setState(StateSteppedDown)
					return
				}
				c.setState(StateStopped)
			},
			OnNewLeader: func(identity string) {
				c.update(func(s *Status) {
					s.Leader = identity
					if identity != c.opts.Identity && s.State != StateLeader {
						s.State = StateStandby
					}
				})
			},
		},
	})
	if err != nil {
		return fmt.Errorf("configure leader election: %w", err)
	}

	elector.Run(electionCtx)
	activeMu.Lock()
	led := becameLead
	activeMu.Unlock()
	if led || elector.IsLeader() {
		<-activeDone
	}
	activeMu.Lock()
	err = activeErr
	activeMu.Unlock()
	if err != nil {
		return err
	}
	if led && ctx.Err() == nil {
		return ErrLeadershipLost
	}
	return nil
}

func (c *Coordinator) setState(state State) {
	c.update(func(s *Status) { s.State = state })
}

func (c *Coordinator) update(change func(*Status)) {
	c.mu.Lock()
	change(&c.status)
	snapshot := c.status
	c.mu.Unlock()
	c.notify(snapshot)
}

func (c *Coordinator) notify(status Status) {
	if c.observe != nil {
		c.observe(status)
	}
}
