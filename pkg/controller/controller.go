/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"os"
	"time"

	clientset "github.com/volcano-sh/kthena/client-go/clientset/versioned"
	autoscaler "github.com/volcano-sh/kthena/pkg/autoscaler/controller"
	modelbooster "github.com/volcano-sh/kthena/pkg/model-booster-controller/controller"
	"github.com/volcano-sh/kthena/pkg/model-booster-controller/utils"
	modelserving "github.com/volcano-sh/kthena/pkg/model-serving-controller/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	volcanoClientSet "volcano.sh/apis/pkg/client/clientset/versioned"

	workloadv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/workload/v1alpha1"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"
)

const (
	defaultLeaseDuration = 15 * time.Second
	defaultRenewDeadline = 10 * time.Second
	defaultRetryPeriod   = 2 * time.Second
	leaderElectionId     = "kthena.controller-manager"
	leaseName            = "lease.kthena.controller-manager"

	ModelServingController = "modelserving"
	ModelBoosterController = "modelbooster"
	AutoscalerController   = "autoscaler"
)

func SetupController(ctx context.Context, cc Config) {
	config, err := clientcmd.BuildConfigFromFlags(cc.MasterURL, cc.Kubeconfig)
	if err != nil {
		klog.Fatalf("build client config: %v", err)
	}
	kubeClient := kubernetes.NewForConfigOrDie(config)
	client := clientset.NewForConfigOrDie(config)
	volcanoClient, err := volcanoClientSet.NewForConfig(config)
	if err != nil {
		klog.Fatalf("failed to create volcano client: %v", err)
	}

	var mc *modelbooster.ModelBoosterController
	var msc *modelserving.ModelServingController
	var ac *autoscaler.AutoscaleController

	for ctrl, enable := range cc.Controllers {
		if enable {
			switch ctrl {
			case ModelBoosterController:
				mc = modelbooster.NewModelBoosterController(kubeClient, client)
			case ModelServingController:
				msc, err = modelserving.NewModelServingController(kubeClient, client, volcanoClient)
				if err != nil {
					klog.Fatalf("failed to create ModelServing controller: %v", err)
				}
			case AutoscalerController:
				namespace, err := utils.GetInClusterNameSpace()
				if err != nil {
					klog.Fatalf("failed to get in-cluster namespace: %v", err)
				}
				ac = autoscaler.NewAutoscaleController(kubeClient, client, namespace)
			}
		}
	}

	startControllers := func(ctx context.Context) {
		if mc != nil {
			go mc.Run(ctx, cc.Workers)
			klog.Info("ModelBooster controller started")
		}
		if msc != nil {
			go msc.Run(ctx, cc.Workers)
			klog.Info("ModelServing controller started")

			// Start LWS Controller if ModelServing is enabled
			go startLWSController(ctx, config)
		}
		if ac != nil {
			go ac.Run(ctx)
			klog.Info("Autoscaler controller started")
		}
	}

	if cc.EnableLeaderElection {
		startedLeading := func(ctx context.Context) {
			startControllers(ctx)
			klog.Info("Start as leader")
		}
		leaderElector, err := initLeaderElector(kubeClient, startedLeading)
		if err != nil {
			panic(err)
		}
		leaderElector.Run(ctx)
	} else {
		startControllers(ctx)
		klog.Info("Started controllers without leader election")
	}
	<-ctx.Done()
}

// initLeaderElector inits a leader elector for leader election
func initLeaderElector(kubeClient kubernetes.Interface, startedLeading func(ctx context.Context)) (*leaderelection.LeaderElector, error) {
	resourceLock, err := newResourceLock(kubeClient)
	if err != nil {
		return nil, err
	}
	leaderElector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          resourceLock,
		LeaseDuration: defaultLeaseDuration,
		RenewDeadline: defaultRenewDeadline,
		RetryPeriod:   defaultRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: startedLeading,
			OnStoppedLeading: func() {
				klog.Error("leader election lost")
			},
		},
		ReleaseOnCancel: false,
		Name:            leaderElectionId,
	})
	if err != nil {
		return nil, err
	}
	return leaderElector, nil
}

// newResourceLock returns a lease lock which is used to elect leader
func newResourceLock(client kubernetes.Interface) (*resourcelock.LeaseLock, error) {
	namespace, err := utils.GetInClusterNameSpace()
	if err != nil {
		return nil, err
	}
	// Leader id, should be unique
	id, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	id = id + "_" + string(uuid.NewUUID())
	return &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}, nil
}

func startLWSController(ctx context.Context, cfg *rest.Config) {
	// Check if LWS CRD exists
	discoveryClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Errorf("Failed to create discovery client for LWS check: %v", err)
		return
	}

	// Check for "leaderworkerset.x-k8s.io/v1"
	exists, err := resourceExists(discoveryClient, "leaderworkerset.x-k8s.io/v1", "LeaderWorkerSet")
	if err != nil {
		klog.Errorf("Failed to check LWS CRD existence: %v", err)
		// Don't return, maybe transient error? But safer to return or retry.
		// For now, let's return.
		return
	}
	if !exists {
		klog.Info("LeaderWorkerSet CRD not found, LWS support disabled")
		return
	}

	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(workloadv1alpha1.AddToScheme(s))
	utilruntime.Must(lwsv1.AddToScheme(s))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: s,
		Metrics: server.Options{
			BindAddress: "0",
		},
		LeaderElection: false,
	})
	if err != nil {
		klog.Errorf("unable to start manager for LWS: %v", err)
		return
	}

	if err = (&modelserving.LWSReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		klog.Errorf("unable to create LWS controller: %v", err)
		return
	}

	klog.Info("Starting LeaderWorkerSet controller")
	if err := mgr.Start(ctx); err != nil {
		klog.Errorf("problem running LWS manager: %v", err)
	}
}

func resourceExists(client kubernetes.Interface, groupVersion string, kind string) (bool, error) {
	resources, err := client.Discovery().ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, r := range resources.APIResources {
		if r.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}
