package coordination

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestNewStartsAsStandbyAndReportsConfiguration(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := c.Status()
	if got.State != StateStandby || got.Identity != "pod-a" || got.LeaseName != "tailscale2otel" {
		t.Fatalf("initial status = %#v, want standby pod-a/tailscale2otel", got)
	}
}

func TestNewRejectsInvalidElectionOptions(t *testing.T) {
	_, err := New(Options{Client: fake.NewSimpleClientset(), Identity: "pod-a", LeaseName: "lease", Namespace: "default", LeaseDuration: 10 * time.Second, RenewDeadline: 10 * time.Second, RetryPeriod: time.Second})
	if err == nil || !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("New invalid timings error = %v, want ErrInvalidOptions", err)
	}
}

func TestNewEnforcesClientGoRetryJitterBound(t *testing.T) {
	base := Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "lease",
		Namespace:     "default",
		LeaseDuration: 13 * time.Second,
		RetryPeriod:   10 * time.Second,
	}

	invalid := base
	invalid.RenewDeadline = 12 * time.Second
	if _, err := New(invalid); err == nil || !errors.Is(err, ErrInvalidOptions) ||
		!strings.Contains(err.Error(), "renew deadline > 1.2 * retry period") {
		t.Fatalf("New(jitter-bound timing) error = %v, want actionable ErrInvalidOptions", err)
	}

	valid := base
	valid.RenewDeadline = 12*time.Second + time.Nanosecond
	if _, err := New(valid); err != nil {
		t.Fatalf("New(timing immediately above jitter bound): %v", err)
	}
}

func TestRunCanceledBeforeElectionDoesNotReportDemotion(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Run(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Run canceled context = %v, want nil", err)
	}
	if got := c.Status().State; got != StateStopped {
		t.Fatalf("status after shutdown = %q, want %q", got, StateStopped)
	}
}

func TestRunWaitsForActiveCallbackShutdown(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 300 * time.Millisecond,
		RenewDeadline: 200 * time.Millisecond,
		RetryPeriod:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	draining := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- c.Run(ctx, func(activeCtx context.Context) error {
			close(started)
			<-activeCtx.Done()
			close(draining)
			<-release
			return nil
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator never acquired the test Lease")
	}
	cancel()
	select {
	case <-draining:
	case <-time.After(2 * time.Second):
		t.Fatal("active callback did not begin shutdown")
	}
	select {
	case err := <-returned:
		close(release)
		t.Fatalf("Run returned before active shutdown completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run after active shutdown = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after active shutdown completed")
	}
}

func TestRunStopsElectionAndPropagatesActiveCallbackError(t *testing.T) {
	c, err := New(Options{
		Client:        fake.NewSimpleClientset(),
		Identity:      "pod-a",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 300 * time.Millisecond,
		RenewDeadline: 200 * time.Millisecond,
		RetryPeriod:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := errors.New("active runtime failed")
	started := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- c.Run(ctx, func(context.Context) error {
			close(started)
			return want
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator never acquired the test Lease")
	}
	select {
	case err := <-returned:
		if !errors.Is(err, want) {
			t.Fatalf("Run error = %v, want active callback error", err)
		}
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-returned
		t.Fatal("Run kept renewing the Lease after the active callback returned")
	}
}

// client-go leader election deliberately has no fencing: it waits for the
// renewal deadline after a Lease changes underneath the leader. The coordinator
// must instead stop the active callback as soon as its Lease observation sees
// deletion or another holder.
func TestRunSelfFencesWhenObservedLeaseChanges(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		wantType   LeaseObservationType
		wantHolder string
		mutate     func(t *testing.T, client *fake.Clientset)
	}{
		{
			name:     "deleted",
			wantType: LeaseObservedDeleted,
			mutate: func(t *testing.T, client *fake.Clientset) {
				t.Helper()
				if err := client.CoordinationV1().Leases("default").Delete(t.Context(), "tailscale2otel", metav1.DeleteOptions{}); err != nil {
					t.Fatalf("delete Lease: %v", err)
				}
			},
		},
		{
			// The fake tracker permits a UID change in an Update, letting this
			// deterministically model an object replacement in one notification.
			// A real replacement is a delete then add and self-fences on delete.
			name:     "replaced",
			wantType: LeaseObservedUpdated,
			mutate: func(t *testing.T, client *fake.Clientset) {
				t.Helper()
				lease, err := client.CoordinationV1().Leases("default").Get(t.Context(), "tailscale2otel", metav1.GetOptions{})
				if err != nil {
					t.Fatalf("get Lease: %v", err)
				}
				lease.UID = types.UID("replacement")
				if _, err := client.CoordinationV1().Leases("default").Update(t.Context(), lease, metav1.UpdateOptions{}); err != nil {
					t.Fatalf("replace Lease object: %v", err)
				}
			},
		},
		{
			name:       "another holder",
			wantType:   LeaseObservedUpdated,
			wantHolder: "pod-b",
			mutate: func(t *testing.T, client *fake.Clientset) {
				t.Helper()
				lease, err := client.CoordinationV1().Leases("default").Get(t.Context(), "tailscale2otel", metav1.GetOptions{})
				if err != nil {
					t.Fatalf("get Lease: %v", err)
				}
				holder := "pod-b"
				lease.Spec.HolderIdentity = &holder
				if _, err := client.CoordinationV1().Leases("default").Update(t.Context(), lease, metav1.UpdateOptions{}); err != nil {
					t.Fatalf("replace Lease holder: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			observations := make(chan LeaseObservation, 4)
			c, err := New(Options{
				Client:        client,
				Identity:      "pod-a",
				LeaseName:     "tailscale2otel",
				Namespace:     "default",
				LeaseDuration: 10 * time.Second,
				RenewDeadline: 8 * time.Second,
				RetryPeriod:   time.Second,
				ObserveLease: func(observation LeaseObservation) {
					observations <- observation
				},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan struct{})
			activeStopped := make(chan struct{})
			returned := make(chan error, 1)
			go func() {
				returned <- c.Run(ctx, func(activeCtx context.Context) error {
					close(started)
					<-activeCtx.Done()
					close(activeStopped)
					return nil
				})
			}()

			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("coordinator never acquired the test Lease")
			}
			tc.mutate(t, client)
			var observed LeaseObservation
			for {
				select {
				case observation := <-observations:
					if observation.Type == tc.wantType && observation.Fenced && (tc.wantHolder == "" || observation.HolderIdentity == tc.wantHolder) {
						observed = observation
						goto observationReceived
					}
				case <-time.After(2 * time.Second):
					cancel()
					<-returned
					t.Fatalf("reusable Lease observer did not report fenced %s observation", tc.wantType)
				}
			}
		observationReceived:
			if tc.wantHolder != "" && observed.PreviousHolderIdentity != "pod-a" {
				t.Fatalf("handover observation = %#v, want previous holder pod-a", observed)
			}

			select {
			case <-activeStopped:
			case <-time.After(2 * time.Second):
				cancel()
				<-returned
				t.Fatal("active callback remained live after its Lease changed; it waited for client-go's renew deadline")
			}
			select {
			case err := <-returned:
				if !errors.Is(err, ErrLeadershipLost) {
					t.Fatalf("Run after self-fencing = %v, want ErrLeadershipLost", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Run did not return after self-fencing the active callback")
			}
		})
	}
}

func TestLeaseObserverStartsFromItsInitialLease(t *testing.T) {
	holder := "pod-a"
	client := fake.NewSimpleClientset(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default", UID: types.UID("lease-a")},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan bool, 1)
	go func() {
		started <- newLeaseObserver(client, "default", "tailscale2otel", "pod-a", nil).Start(ctx)
	}()
	select {
	case ok := <-started:
		if !ok {
			t.Fatal("observer did not accept its initial Lease")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observer did not complete its initial Lease list")
	}
}

func TestLeaseObserverObservesStandbyBeforeElection(t *testing.T) {
	holder := "pod-a"
	client := fake.NewSimpleClientset(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default", UID: types.UID("lease-a")},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan bool, 1)
	go func() {
		started <- newLeaseObserver(client, "default", "tailscale2otel", "pod-b", nil).Start(ctx)
	}()
	select {
	case ok := <-started:
		if !ok {
			t.Fatal("standby observer did not accept the existing Lease")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("standby observer did not complete its initial Lease list")
	}
}

func TestRunObservesLeaseWhileStandby(t *testing.T) {
	holder := "pod-a"
	client := fake.NewSimpleClientset(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default", UID: types.UID("lease-a")},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	})
	observations := make(chan LeaseObservation, 4)
	c, err := New(Options{
		Client:        client,
		Identity:      "pod-b",
		LeaseName:     "tailscale2otel",
		Namespace:     "default",
		LeaseDuration: 10 * time.Second,
		RenewDeadline: 8 * time.Second,
		RetryPeriod:   time.Second,
		ObserveLease: func(observation LeaseObservation) {
			observations <- observation
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		returned <- c.Run(ctx, func(context.Context) error {
			t.Error("standby callback started")
			return nil
		})
	}()
	observation := waitLeaseObservation(t, observations, func(observation LeaseObservation) bool {
		return observation.Initial && observation.HolderIdentity == "pod-a"
	})
	if observation.CompletedHandover || observation.Fenced {
		t.Fatalf("standby observation = %#v, want an initial non-fencing non-handover event", observation)
	}
	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run after standby cancellation = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("standby Run did not stop after parent cancellation")
	}
}

func TestLeaseObserverCountsOnlyIncomingHandover(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial string
		mutate  func(t *testing.T, client *fake.Clientset)
	}{
		{
			name:    "initial owner is not a handover",
			initial: "pod-b",
		},
		{
			name:    "holder transition to this process",
			initial: "pod-a",
			mutate: func(t *testing.T, client *fake.Clientset) {
				t.Helper()
				lease, err := client.CoordinationV1().Leases("default").Get(t.Context(), "tailscale2otel", metav1.GetOptions{})
				if err != nil {
					t.Fatalf("get Lease: %v", err)
				}
				holder := "pod-b"
				lease.Spec.HolderIdentity = &holder
				if _, err := client.CoordinationV1().Leases("default").Update(t.Context(), lease, metav1.UpdateOptions{}); err != nil {
					t.Fatalf("update Lease: %v", err)
				}
			},
		},
		{
			name:    "delete recreate to this process",
			initial: "pod-a",
			mutate: func(t *testing.T, client *fake.Clientset) {
				t.Helper()
				if err := client.CoordinationV1().Leases("default").Delete(t.Context(), "tailscale2otel", metav1.DeleteOptions{}); err != nil {
					t.Fatalf("delete Lease: %v", err)
				}
				holder := "pod-b"
				if _, err := client.CoordinationV1().Leases("default").Create(t.Context(), &coordinationv1.Lease{
					ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default", UID: types.UID("replacement")},
					Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
				}, metav1.CreateOptions{}); err != nil {
					t.Fatalf("recreate Lease: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holder := tc.initial
			client := fake.NewSimpleClientset(&coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default", UID: types.UID("lease-a")},
				Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
			})
			observations := make(chan LeaseObservation, 8)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			observer := newLeaseObserver(client, "default", "tailscale2otel", "pod-b", func(observation LeaseObservation) {
				observations <- observation
			})
			if !observer.Start(ctx) {
				t.Fatal("Start returned false")
			}
			initial := waitLeaseObservation(t, observations, func(observation LeaseObservation) bool { return observation.Initial })
			if initial.CompletedHandover {
				t.Fatalf("initial observation = %#v, must not count a handover", initial)
			}
			if tc.mutate == nil {
				return
			}
			tc.mutate(t, client)
			handover := waitLeaseObservation(t, observations, func(observation LeaseObservation) bool { return observation.CompletedHandover })
			if handover.PreviousHolderIdentity != "pod-a" || handover.HolderIdentity != "pod-b" {
				t.Fatalf("handover = %#v, want pod-a -> pod-b", handover)
			}
		})
	}
}

func TestLeaseObserverStartsBeforeAnAbsentLeaseExists(t *testing.T) {
	client := fake.NewSimpleClientset()
	observations := make(chan LeaseObservation, 4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observer := newLeaseObserver(client, "default", "tailscale2otel", "pod-b", func(observation LeaseObservation) {
		observations <- observation
	})
	if !observer.Start(ctx) {
		t.Fatal("Start returned false for an absent Lease")
	}
	initial := waitLeaseObservation(t, observations, func(observation LeaseObservation) bool { return observation.Initial })
	if initial.Type != LeaseObservedAbsent || initial.CompletedHandover {
		t.Fatalf("initial absent observation = %#v, want non-handover absence", initial)
	}
	holder := "pod-b"
	if _, err := client.CoordinationV1().Leases("default").Create(t.Context(), &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default", UID: types.UID("lease-b")},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create Lease: %v", err)
	}
	added := waitLeaseObservation(t, observations, func(observation LeaseObservation) bool {
		return observation.Type == LeaseObservedAdded && !observation.Initial
	})
	if added.CompletedHandover {
		t.Fatalf("first acquisition = %#v, must not count a handover", added)
	}
}

func TestLeaseObserverArmRejectsChangeObservedBetweenReadAndArm(t *testing.T) {
	holder := "pod-a"
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "tailscale2otel", Namespace: "default", UID: types.UID("lease-a")},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}
	client := fake.NewSimpleClientset(lease)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observer := newLeaseObserver(client, "default", "tailscale2otel", "pod-a", nil)
	if !observer.Start(ctx) {
		t.Fatal("Start returned false")
	}
	reacted := false
	client.PrependReactor("get", "leases", func(k8stesting.Action) (bool, runtime.Object, error) {
		old := lease.DeepCopy()
		changed := lease.DeepCopy()
		other := "pod-b"
		changed.Spec.HolderIdentity = &other
		if !reacted {
			reacted = true
			// This runs at the precise seam after Arm's GET returned the old
			// owner but before it can publish the armed boundary.
			observer.update(old, changed)
			return true, old, nil
		}
		return true, changed, nil
	})
	if observer.Arm(ctx, func() { t.Error("unarmed callback was fenced") }) {
		t.Fatal("Arm accepted an ownership read raced by an observed holder change")
	}
}

func waitLeaseObservation(t *testing.T, observations <-chan LeaseObservation, match func(LeaseObservation) bool) LeaseObservation {
	t.Helper()
	for {
		select {
		case observation := <-observations:
			if match(observation) {
				return observation
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Lease observation")
		}
	}
}
