package coordination

import (
	"context"
	"sync"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// LeaseObservationType identifies the Kubernetes notification that produced a
// LeaseObservation. Observations are delivered in order for this Lease.
type LeaseObservationType string

const (
	LeaseObservedAbsent  LeaseObservationType = "absent"
	LeaseObservedAdded   LeaseObservationType = "added"
	LeaseObservedUpdated LeaseObservationType = "updated"
	LeaseObservedDeleted LeaseObservationType = "deleted"
)

// LeaseObservation is one observation from the live Lease. It is intentionally
// broader than fencing: a handover counter uses CompletedHandover without
// creating another watch.
//
// Fenced is true only when an armed active callback was canceled. client-go
// deliberately does not fence a leader, so this observer locally fences when
// the active Lease vanishes, changes UID, or changes holder. It cannot make
// distributed ownership atomic and it never mutates Kubernetes or the tailnet.
type LeaseObservation struct {
	Type    LeaseObservationType
	Initial bool
	// Identity is the identity of this observing process, not necessarily the
	// Lease holder in this observation.
	Identity               string
	UID                    types.UID
	HolderIdentity         string
	PreviousUID            types.UID
	PreviousHolderIdentity string
	Fenced                 bool
	// CompletedHandover is true exactly once for a non-initial transition from
	// a different non-empty holder to this observer's identity. A Lease delete
	// preserves the prior holder, so a delete/recreate handover is counted on
	// the incoming Add. Initial state and a process restart never count.
	CompletedHandover bool
}

// leaseObserver is the Coordinator's one process-lifetime Lease ListWatch. It
// observes standby and active transitions alike; Arm and Disarm only attach
// local self-fencing to the active callback.
type leaseObserver struct {
	client    kubernetes.Interface
	namespace string
	leaseName string
	identity  string
	onObserve func(LeaseObservation)

	mu          sync.Mutex
	sequence    uint64
	present     bool
	currentUID  types.UID
	current     string
	lastHolder  string
	armed       bool
	expectedUID types.UID
	onFence     func()
}

func newLeaseObserver(client kubernetes.Interface, namespace, leaseName, identity string, onObserve func(LeaseObservation)) *leaseObserver {
	return &leaseObserver{
		client:    client,
		namespace: namespace,
		leaseName: leaseName,
		identity:  identity,
		onObserve: onObserve,
	}
}

// Start runs the observer's one ListWatch until ctx ends. It accepts an absent
// Lease because election can legitimately create one after the standby watcher
// is running.
func (o *leaseObserver) Start(ctx context.Context) bool {
	selector := fields.OneTermEqualSelector("metadata.name", o.leaseName).String()
	list := func(requestCtx context.Context, options metav1.ListOptions) (runtime.Object, error) {
		options.FieldSelector = selector
		return o.client.CoordinationV1().Leases(o.namespace).List(requestCtx, options)
	}
	watchLease := func(requestCtx context.Context, options metav1.ListOptions) (watch.Interface, error) {
		options.FieldSelector = selector
		return o.client.CoordinationV1().Leases(o.namespace).Watch(requestCtx, options)
	}
	lw := &cache.ListWatch{
		ListWithContextFunc:  list,
		WatchFuncWithContext: watchLease,
	}
	// The wrapper keeps streaming-list support on real clients and disables it
	// for client-go's fake tracker, which cannot emit its initial bookmark.
	informer := cache.NewSharedIndexInformer(cache.ToListWatcherWithWatchListSemantics(lw, o.client), &coordinationv1.Lease{}, 0, cache.Indexers{})
	initialReady := make(chan struct{})
	var readyOnce sync.Once
	defer readyOnce.Do(func() { close(initialReady) })
	awaitInitial := func() bool {
		select {
		case <-initialReady:
			return true
		case <-ctx.Done():
			return false
		}
	}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj interface{}, initial bool) {
			if !initial && awaitInitial() {
				o.add(asLease(obj), false)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if awaitInitial() {
				o.update(asLease(oldObj), asLease(newObj))
			}
		},
		DeleteFunc: func(obj interface{}) {
			if awaitInitial() {
				o.delete(asLease(obj))
			}
		},
	})
	if err != nil {
		return false
	}
	go informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return false
	}
	item, exists, err := informer.GetStore().GetByKey(o.namespace + "/" + o.leaseName)
	if err != nil || !exists {
		o.absent(true)
	} else {
		o.add(asLease(item), true)
	}
	readyOnce.Do(func() { close(initialReady) })
	return ctx.Err() == nil
}

// Arm makes an active callback self-fencing. The sequence check ensures a
// watcher event observed between the ownership read and arming makes us retry;
// an event after arming sees the expected UID and cancels immediately.
func (o *leaseObserver) Arm(ctx context.Context, onFence func()) bool {
	for {
		o.mu.Lock()
		before := o.sequence
		o.mu.Unlock()
		lease, err := o.client.CoordinationV1().Leases(o.namespace).Get(ctx, o.leaseName, metav1.GetOptions{})
		if err != nil || leaseHolder(lease) != o.identity {
			return false
		}
		o.mu.Lock()
		if o.sequence != before {
			o.mu.Unlock()
			continue
		}
		o.armed = true
		o.expectedUID = lease.UID
		o.onFence = onFence
		o.mu.Unlock()
		return true
	}
}

func (o *leaseObserver) Disarm() {
	o.mu.Lock()
	o.armed = false
	o.expectedUID = ""
	o.onFence = nil
	o.mu.Unlock()
}

func (o *leaseObserver) absent(initial bool) {
	o.observe(LeaseObservation{Type: LeaseObservedAbsent, Initial: initial}, false)
}

func (o *leaseObserver) add(lease *coordinationv1.Lease, initial bool) {
	if lease == nil {
		return
	}
	o.observe(LeaseObservation{
		Type:           LeaseObservedAdded,
		Initial:        initial,
		UID:            lease.UID,
		HolderIdentity: leaseHolder(lease),
	}, true)
}

func (o *leaseObserver) update(oldLease, newLease *coordinationv1.Lease) {
	if newLease == nil {
		return
	}
	o.observe(LeaseObservation{
		Type:                   LeaseObservedUpdated,
		UID:                    newLease.UID,
		HolderIdentity:         leaseHolder(newLease),
		PreviousUID:            leaseUID(oldLease),
		PreviousHolderIdentity: leaseHolder(oldLease),
	}, true)
}

func (o *leaseObserver) delete(lease *coordinationv1.Lease) {
	o.observe(LeaseObservation{
		Type:                   LeaseObservedDeleted,
		UID:                    leaseUID(lease),
		HolderIdentity:         leaseHolder(lease),
		PreviousUID:            leaseUID(lease),
		PreviousHolderIdentity: leaseHolder(lease),
	}, false)
}

func (o *leaseObserver) observe(observation LeaseObservation, present bool) {
	o.mu.Lock()
	observation.Identity = o.identity
	previousObservedHolder := o.current
	if previousObservedHolder == "" && !o.present {
		previousObservedHolder = o.lastHolder
	}
	if observation.PreviousUID == "" {
		observation.PreviousUID = o.currentUID
	}
	if observation.PreviousHolderIdentity == "" {
		observation.PreviousHolderIdentity = previousObservedHolder
	}
	if present {
		observation.CompletedHandover = !observation.Initial && previousObservedHolder != "" && previousObservedHolder != observation.HolderIdentity && observation.HolderIdentity == o.identity
		o.present = true
		o.currentUID = observation.UID
		o.current = observation.HolderIdentity
		if observation.HolderIdentity != "" {
			o.lastHolder = observation.HolderIdentity
		}
	} else {
		o.present = false
		o.currentUID = ""
		o.current = ""
		if observation.HolderIdentity != "" {
			o.lastHolder = observation.HolderIdentity
		}
	}
	o.sequence++
	if o.armed && (!present || observation.UID != o.expectedUID || observation.HolderIdentity != o.identity) {
		observation.Fenced = true
	}
	onFence := o.onFence
	onObserve := o.onObserve
	o.mu.Unlock()
	if observation.Fenced && onFence != nil {
		onFence()
	}
	if onObserve != nil {
		onObserve(observation)
	}
}

func asLease(obj interface{}) *coordinationv1.Lease {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	lease, _ := obj.(*coordinationv1.Lease)
	return lease
}

func leaseHolder(lease *coordinationv1.Lease) string {
	if lease == nil || lease.Spec.HolderIdentity == nil {
		return ""
	}
	return *lease.Spec.HolderIdentity
}

func leaseUID(lease *coordinationv1.Lease) types.UID {
	if lease == nil {
		return ""
	}
	return lease.UID
}
