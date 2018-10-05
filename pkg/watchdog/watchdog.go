package watchdog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	corev1api "k8s.io/api/core/v1"
	types "k8s.io/api/core/v1"
	//apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerytypes "k8s.io/apimachinery/pkg/types"

	kubeinformers "k8s.io/client-go/informers"
	kubecoreinformers "k8s.io/client-go/informers/core/v1"
	extensionsinformers "k8s.io/client-go/informers/extensions/v1beta1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	api "k8s.io/kubernetes/pkg/apis/core"
)

type nodeStage string

const (
	stageOne   nodeStage = "stage1" // not ready
	stageTwo   nodeStage = "stage2" // Gets here if: stage 1 for  grace period (start monitoring of powerstatus)
	stageThree nodeStage = "stage3" // gets here if: shutdowned
	// stage 4 is final. We remove disks if we hit stage 4. and it will be removed from tracked nodes
	// node can stay in stage 2 forever if kubelet hangs
	// node can be removed from any stage if it become "ready"
	// nodes will be go back to stage two if they were powered on
)

type workItemType string

const (
	addWorkItem    workItemType = "add"
	updateWorkItem workItemType = "update"
	deleteWorkItem workItemType = "delete"
)

// CloudHandler defines what is expected
// from the components that interfaces with
// cloud control plane
type CloudHandler interface {
	// true if the node is not running/power down
	IsNodePoweredDown(ctx context.Context, nodeName string, nodeProviderId string) (bool, error)
	// Detaches all disks (Except OS disk)
	DetachDisks(ctx context.Context, nodeName string, nodeProviderId string) error
	RestartNode(ctx context.Context, nodeName string, nodeProviderId string) error
}

type WatchDog interface {
	Run(stopCh <-chan struct{}) error
}

type WatchDogConfig struct {
	KubeConfigPath string

	RunInterval  time.Duration
	SyncInterval time.Duration

	StageOneDuration   time.Duration
	StageTwoDuration   time.Duration
	StageThreeDuration time.Duration

	ForceRestartStageTwo    bool
	StageTwoRestartDuration time.Duration

	MaxNodeProcessingTime time.Duration
	TrackMasters          bool

	LeaderElectionNamespace string
	LeaderElectionTtl       time.Duration
	LeaderElectionId        string
	ThisLeaderName          string
}
type trackedNode struct {
	node        *corev1api.Node
	lastUpdated time.Time
	stage       nodeStage
}

type workItem struct {
	newItem  interface{}
	oldItem  interface{}
	workType workItemType
}

type watchDogRunner struct {
	WatchDogConfig
	clientSet       *kubernetes.Clientset
	informerFactory kubeinformers.SharedInformerFactory
	nodeInformer    kubecoreinformers.NodeInformer
	dsInformer      extensionsinformers.DaemonSetInformer
	trackedNodes    map[string]*trackedNode
	workqueue       workqueue.RateLimitingInterface
	cloud           CloudHandler
	recorder        record.EventRecorder
	le              *leaderelection.LeaderElector
}

func NewWatchdog(cfg WatchDogConfig, cloud CloudHandler) (WatchDog, error) {

	config, err := clientcmd.BuildConfigFromFlags("", cfg.KubeConfigPath)
	if nil != err {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if nil != err {
		return nil, err
	}

	// Setup
	informerFactory := kubeinformers.NewSharedInformerFactory(clientset, cfg.SyncInterval)
	nodeinformer := informerFactory.Core().V1().Nodes()
	dsInformer := informerFactory.Extensions().V1beta1().DaemonSets()

	// recorder
	eventBroadcaster := record.NewBroadcaster()
	//	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1api.EventSource{Component: "kubernetes-watchdog"})

	d := &watchDogRunner{
		WatchDogConfig:  cfg,
		nodeInformer:    nodeinformer,
		dsInformer:      dsInformer,
		informerFactory: informerFactory,
		trackedNodes:    make(map[string]*trackedNode),
		workqueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "changeNodes"),
		cloud:           cloud,
		clientSet:       clientset,
		recorder:        recorder,
	}
	// glow for watcher events
	nodeinformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			wi := workItem{
				newItem:  obj,
				workType: addWorkItem,
			}
			d.workqueue.AddRateLimited(wi)
		},
		UpdateFunc: func(old, new interface{}) {
			wi := workItem{
				newItem:  new,
				oldItem:  old,
				workType: updateWorkItem,
			}
			d.workqueue.AddRateLimited(wi)
		},
		DeleteFunc: func(obj interface{}) {
			wi := workItem{
				newItem:  obj,
				workType: deleteWorkItem,
			}
			d.workqueue.AddRateLimited(wi)
		},
	})

	if err := d.setupLeaderElection(); nil != err {
		return nil, err
	}
	return d, nil
}

func (d *watchDogRunner) Run(stopCh <-chan struct{}) error {
	// Older kubernetes clients does not have
	// coordinated cancelation for leader election
	// TODO: find a way to link outer stop channel
	// with stop channel used by leader elector
	d.le.Run()
	return nil
}

// this stop channel is created by the
// leader elector
func (d *watchDogRunner) runWatchDog(stopCh <-chan struct{}) {
	defer d.workqueue.ShutDown()
	go d.informerFactory.Start(stopCh)
	// Wait for sync
	glog.Infof("Syncing cache")
	if ok := cache.WaitForCacheSync(stopCh, d.nodeInformer.Informer().HasSynced, d.dsInformer.Informer().HasSynced); !ok {
		runtime.HandleError(fmt.Errorf("failed to wait for caches to sync"))
	}

	glog.Infof("Syncing cache -- completed")

	go d.informerFactory.Start(stopCh)

	glog.Infof("Starting processing at %v interval", d.RunInterval)
	wait.PollUntil(d.RunInterval, func() (bool, error) {
		d.processQueue()
		d.advanceStages()
		return false, nil
	}, stopCh)

}

func (d *watchDogRunner) setupLeaderElection() error {
	rl, err := resourcelock.New(resourcelock.EndpointsResourceLock,
		d.LeaderElectionNamespace,
		d.LeaderElectionId,
		d.clientSet.CoreV1(),
		resourcelock.ResourceLockConfig{
			Identity:      d.ThisLeaderName,
			EventRecorder: d.recorder,
		})

	callbacks := leaderelection.LeaderCallbacks{
		OnStartedLeading: d.runWatchDog,
		OnStoppedLeading: func() {
			runtime.HandleError(fmt.Errorf("Lost Leader Lease"))
		},
	}
	config := leaderelection.LeaderElectionConfig{
		LeaseDuration: d.LeaderElectionTtl,
		RenewDeadline: d.LeaderElectionTtl / 2,
		RetryPeriod:   d.LeaderElectionTtl / 4,
		Callbacks:     callbacks,
		Lock:          rl,
	}

	if nil != err {
		return err
	}

	le, err := leaderelection.NewLeaderElector(config)
	if nil != err {
		return err
	}

	d.le = le
	return nil
}

// events queue processor
// adds node to tracked to remove based
// on node status change
func (d *watchDogRunner) processQueue() {
	maxWIPerRun := 10000 // for a large cluster that is two status reporting in one cycle
	currentRun := 0
	glog.Infof("Start: queue processing")

	for {
		currentRun = currentRun + 1
		if currentRun > maxWIPerRun {
			break
		}

		if 0 == d.workqueue.Len() {
			break
		}
		obj, shutdown := d.workqueue.Get()
		if shutdown {
			break
		}

		err := func(obj interface{}) error {
			defer d.workqueue.Done(obj)
			var ok bool
			var wi workItem

			if wi, ok = obj.(workItem); !ok {
				d.workqueue.Forget(obj)
				glog.Errorf("unexpected value in workqueue but got %v was expecting a work item, this work item will be ignored")
				runtime.HandleError(fmt.Errorf("unexpected value in workqueue but got %v was expecting a work item, this work item will be ignored", obj))
				return nil
			}

			node := wi.newItem.(*corev1api.Node).DeepCopy()
			_, exist := d.trackedNodes[string(node.Name)]

			switch wi.workType {
			case addWorkItem, updateWorkItem:
				if isNodeReady(node) {
					// if tracked remove it
					if exist {
						logLine := fmt.Sprintf("node: %s was tracked, and just entered Ready status. It will not be no longer tracked", node.Name)
						d.recordEvent(logLine, node, corev1api.EventTypeNormal)
						glog.Infof(logLine)
						delete(d.trackedNodes, node.Name)
					}
				} else {
					// We only track masters if we have
					// been asked to
					if isMasterNode(node) && false == d.TrackMasters {
						logLine := fmt.Sprintf("master node: %s entered not Ready status and track masters [DISABLED], ignoring", node.Name)
						d.recordEvent(logLine, node, corev1api.EventTypeWarning)
						glog.Infof(logLine)
						return nil
					}

					if !exist {
						d.trackedNodes[node.Name] = &trackedNode{
							stage:       stageOne,
							lastUpdated: time.Now().UTC(),
							node:        node,
						}
						logLine := fmt.Sprintf("node: %s entered not Ready status, and it will be tracked", node.Name)
						d.recordEvent(logLine, node, corev1api.EventTypeWarning)
						glog.Infof(logLine)
					}
				}

			case deleteWorkItem:
				if exist {
					delete(d.trackedNodes, node.Name)
					logLine := fmt.Sprintf("node: %s was tracked but deleted from cluster, it will no longer be tracked", node.Name)
					glog.Infof(logLine)
				}
			}
			d.workqueue.Forget(obj)
			return nil
		}(obj)

		if err != nil {
			glog.Infof("Runtime processing workqueue: %v", err.Error())
			runtime.HandleError(err)
		}
	}
	glog.Infof("End: queue processing - processed %d event", currentRun)
}

func (d *watchDogRunner) deleteNodePods(nodeName string) error {
	selector := fields.OneTermEqualSelector(api.PodHostField, nodeName).String()
	options := metav1.ListOptions{FieldSelector: selector}
	pods, err := d.clientSet.CoreV1().Pods(metav1.NamespaceAll).List(options)
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		// Set reason and message in the pod object.
		if err = d.setPodMessage(&pod, nodeName); err != nil {
			return err
		}

		dsStore := d.dsInformer.Lister()
		// ignore daemonsets' pods
		_, err := dsStore.GetPodDaemonSets(&pod)
		if err == nil {
			glog.Infof("Node: %s, pod: %s/%s part of a daemonset and will be ignored in node deletion logic", nodeName, pod.Namespace, pod.Name)
			continue
		}

		glog.Infof("deleting pod %v/%v on node %s", pod.Namespace, pod.Name, nodeName)
		zeroGrace := int64(0)
		deleteOptions := &metav1.DeleteOptions{
			GracePeriodSeconds: &zeroGrace,
		}
		if err := d.clientSet.CoreV1().Pods(pod.Namespace).Delete(pod.Name, deleteOptions); err != nil {
			return err
		}
	}

	return nil
}

func (d *watchDogRunner) setPodMessage(pod *corev1api.Pod, nodeName string) error {
	pod.Status.Reason = "kubernetes-watchdog observed that node is powered down"
	pod.Status.Message = fmt.Sprintf("node %s was observed as powered down by kubernetes-watchdog", nodeName)

	var err error
	if _, err = d.clientSet.CoreV1().Pods(pod.Namespace).UpdateStatus(pod); err != nil {
		return err
	}
	return nil
}

func (d *watchDogRunner) clearNodeVolumes(nodeName string) error {
	//attach-detach controller hangs to the node object
	// even when it is deleted. patch the node with
	// .VolumesAttached = [] and .VolumesIsUse = []

	nodesClient := d.clientSet.CoreV1().Nodes()
	node, err := nodesClient.Get(nodeName, metav1.GetOptions{})
	if nil != err {
		return err
	}

	oldData, err := json.Marshal(corev1api.Node{
		Status: corev1api.NodeStatus{
			VolumesInUse:    node.Status.VolumesInUse,
			VolumesAttached: node.Status.VolumesAttached,
		},
	})

	if nil != err {
		return err
	}

	newData, err := json.Marshal(corev1api.Node{
		Status: corev1api.NodeStatus{
			VolumesInUse:    make([]corev1api.UniqueVolumeName, 0),
			VolumesAttached: make([]corev1api.AttachedVolume, 0),
		},
	})

	if nil != err {
		return err
	}

	patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, corev1api.Node{})
	if nil != err {
		return err
	}

	glog.Infof("clearing [VolumesInUse] and [VolumesAttached] on node: %s", nodeName)
	_, err = d.clientSet.CoreV1().Nodes().Patch(nodeName, apimachinerytypes.StrategicMergePatchType, patchBytes, "status")
	return err
}

// deletes pods
// patches the node with no disks attached/used
// deletes the node object
func (d *watchDogRunner) clusterDeleteNode(nodeName string) error {
	nodesClient := d.clientSet.CoreV1().Nodes()
	return nodesClient.Delete(nodeName, nil)
}

func (d *watchDogRunner) advanceStages() {
	for k, v := range d.trackedNodes {
		// total time allocated to process this node
		ctx, _ := context.WithTimeout(context.Background(), d.MaxNodeProcessingTime)

		switch v.stage {
		case stageOne:
			// Should we advance this node to stagetwo?
			if time.Now().UTC().Sub(v.lastUpdated) > d.StageOneDuration {
				v.stage = stageTwo
				v.lastUpdated = time.Now().UTC()
				logLine := fmt.Sprintf("node: %s spent more than %v in stage one, %s moved to stage two", string(v.node.Name), d.StageOneDuration, string(v.node.Name))
				d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
				glog.Infof(logLine)

			}
			continue
		case stageTwo:
			// if node became "Ready" it will be remove before
			// we hit this check
			if time.Now().UTC().Sub(v.lastUpdated) > d.StageTwoDuration {
				down, err := d.cloud.IsNodePoweredDown(ctx, string(v.node.Name), v.node.Spec.ProviderID)
				if nil != err {
					logLine := fmt.Sprintf("failed to check power status for node: %s with error:%s. will ignore it for this run ", string(v.node.Name), err.Error())
					d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
					glog.Infof(logLine)
					continue
				}

				if down {
					// move to stage 3
					v.stage = stageThree
					v.lastUpdated = time.Now().UTC()

					logLine := fmt.Sprintf("node: %s spent more than %v in stage two and is [POWERED OFF], %s  moved to stage three", string(v.node.Name), d.StageOneDuration, string(v.node.Name))
					d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
					glog.Infof(logLine)
				} else {
					// if force restart for un ready nodes
					// enabled go into restart logic
					if d.ForceRestartStageTwo {
						if time.Now().UTC().Sub(v.lastUpdated) > d.StageTwoRestartDuration {
							logLine := fmt.Sprintf("node: %s spent more than %v in stage two and is NOT [POWERED OFF], [Force restarte enabled] %s will restart ",
								string(v.node.Name),
								d.StageTwoRestartDuration,
								string(v.node.Name))
							d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
							glog.Infof(logLine)

							d.cloud.RestartNode(ctx, string(v.node.Name), v.node.Spec.ProviderID)
							glog.Infof("node: %s was restarted successfully", string(v.node.Name))

							// we turn it back to stage one
							// so we wouldn't keep on restarting it
							v.stage = stageOne
							v.lastUpdated = time.Now().UTC()
						}
					} else {
						// node will stay in this state forever
						d.recordEvent("watchdog suspects this node has hanged/kubelet is down. watchdog restart is disabled. This node require manual intervention", v.node, corev1api.EventTypeWarning)
						glog.Infof("node: %s spent more than %v in stage two and is NOT [POWERED OFF], %s will stay in stage two", string(v.node.Name), d.StageTwoDuration, string(v.node.Name))
						glog.Infof("node: %s has either hanged or kubelet crashed/stopped and never started  [Force restart is disabled]", string(v.node.Name))
					}
				}
			}
			continue
		case stageThree:
			if time.Now().UTC().Sub(v.lastUpdated) > d.StageThreeDuration {
				down, err := d.cloud.IsNodePoweredDown(ctx, string(v.node.Name), v.node.Spec.ProviderID)
				if nil != err {
					glog.Infof("failed to check power status for node: %s. will ignore it for this run ", string(v.node.Name))
					continue
				}

				if down {
					logLine := fmt.Sprintf("node: %s reached stage three and stayed for %v still powered down, will delete from cluster and detach disks", string(v.node.Name), d.StageThreeDuration)
					d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
					glog.Infof(logLine)

					// delete pods
					if err := d.deleteNodePods(string(v.node.Name)); nil != err {
						glog.Errorf("node: %s failed to delete pods  with error: %v. will try again next run", string(v.node.Name), err.Error())
						continue
					}
					logLine = fmt.Sprintf("pods on node %s were deleted", string(v.node.Name))
					d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)

					glog.Infof(logLine)

					// Clear node volumeInUse and VolumesAttached
					if err := d.clearNodeVolumes(v.node.Name); nil != err {
						glog.Infof("Error clearing volumes on node :%s. will try next run", string(v.node.Name))
						continue
					}
					logLine = fmt.Sprintf("node %s [VolumesInUse] [VolumesAttached] cleared", string(v.node.Name))
					d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
					glog.Infof(logLine)

					// detach disks
					if err := d.cloud.DetachDisks(ctx, string(v.node.Name), v.node.Spec.ProviderID); nil != err {
						glog.Errorf("node: %s failed to detach disks with error: %v. will try again next run", string(v.node.Name), err.Error())
						continue
					}
					logLine = fmt.Sprintf("node %s disks were detached", string(v.node.Name))
					d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
					glog.Infof(logLine)

					// delete from cluster
					if err := d.clusterDeleteNode(string(v.node.Name)); nil != err {
						glog.Errorf("node: %s failed to delete from cluster with error: %v. will try again next run", string(v.node.Name), err.Error())
						continue
					}
					glog.Infof("node: %s deleted from cluster", string(v.node.Name))

					// remove tracking
					delete(d.trackedNodes, k)
				} else {
					// node started, move it stage one
					logLine := fmt.Sprintf("node: %s reached stage three and stayed for %d then was powered back on. will move to stage one and keep tracking", string(v.node.Name), d.StageThreeDuration)
					d.recordEvent(logLine, v.node, corev1api.EventTypeWarning)
					glog.Infof(logLine)

					// keep watching. if it
					// recovers it will be removed
					// from tracked node list
					v.stage = stageOne
					v.lastUpdated = time.Now().UTC()
				}
			}
		}
	}
}

func (d *watchDogRunner) recordEvent(logLine string, node *corev1api.Node, eventType string) {
	// Create a Hollow Proxy instance.
	nodeRef := &corev1api.ObjectReference{
		Kind:      "Node",
		Name:      node.Name,
		UID:       apimachinerytypes.UID(node.Name),
		Namespace: "",
	}
	d.recorder.Event(nodeRef, eventType, "kubernetes-watchdog", logLine)
}

func isMasterNode(node *corev1api.Node) bool {
	for k, v := range node.Labels {
		if k == "kubernetes.io/role" && v == "master" {
			return true
		}
	}

	return false
}
func isNodeReady(node *corev1api.Node) bool {
	for _, v := range node.Status.Conditions {
		if types.NodeReady == v.Type && types.ConditionTrue == v.Status {
			return true
		}
	}
	return false
}
