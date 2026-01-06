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
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"

	workloadv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/workload/v1alpha1"
)

// LWSReconciler reconciles a LeaderWorkerSet object
type LWSReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=leaderworkerset.x-k8s.io,resources=leaderworkersets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=leaderworkerset.x-k8s.io,resources=leaderworkersets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=leaderworkerset.x-k8s.io,resources=leaderworkersets/finalizers,verbs=update
//+kubebuilder:rbac:groups=workload.volcano.sh,resources=modelservings,verbs=get;list;watch;create;update;patch;delete

func (r *LWSReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch LeaderWorkerSet
	lws := &lwsv1.LeaderWorkerSet{}
	if err := r.Get(ctx, req.NamespacedName, lws); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Define ModelServing Name (same as LWS)
	msName := lws.Name
	ms := &workloadv1alpha1.ModelServing{}
	err := r.Get(ctx, types.NamespacedName{Name: msName, Namespace: lws.Namespace}, ms)

	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// 3. Create or Update ModelServing
	if errors.IsNotFound(err) {
		// Create
		ms = r.constructModelServing(lws)
		if err := controllerutil.SetControllerReference(lws, ms, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("Creating ModelServing", "name", ms.Name)
		if err := r.Create(ctx, ms); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		// Update
		desiredMs := r.constructModelServing(lws)
		if !reflect.DeepEqual(ms.Spec, desiredMs.Spec) {
			ms.Spec = desiredMs.Spec
			logger.Info("Updating ModelServing", "name", ms.Name)
			if err := r.Update(ctx, ms); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// 4. Update LWS Status
	if err := r.updateLWSStatus(ctx, lws, ms); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *LWSReconciler) constructModelServing(lws *lwsv1.LeaderWorkerSet) *workloadv1alpha1.ModelServing {
	// Translation logic based on the proposal
	replicas := int32(1)
	if lws.Spec.Replicas != nil {
		replicas = *lws.Spec.Replicas
	}

	// Helper to convert PodTemplateSpec
	convertTemplate := func(src corev1.PodTemplateSpec) workloadv1alpha1.PodTemplateSpec {
		return workloadv1alpha1.PodTemplateSpec{
			Metadata: &workloadv1alpha1.Metadata{
				Labels:      src.ObjectMeta.Labels,
				Annotations: src.ObjectMeta.Annotations,
			},
			Spec: src.Spec,
		}
	}

	// Helper to convert pointer PodTemplateSpec
	convertTemplatePtr := func(src *corev1.PodTemplateSpec) *workloadv1alpha1.PodTemplateSpec {
		if src == nil {
			return nil
		}
		t := convertTemplate(*src)
		return &t
	}

	// Worker Role Size calculation
	workerSize := int32(1)
	if lws.Spec.LeaderWorkerTemplate.Size != nil {
		workerSize = *lws.Spec.LeaderWorkerTemplate.Size
	}
	// Worker replicas = Size - 1 (Assuming 1 leader)
	workerReplicas := workerSize - 1
	if workerReplicas < 0 {
		workerReplicas = 0
	}

	roleReplicas := int32(1)

	// Create a single role that contains both Leader (Entry) and Worker templates
	role := workloadv1alpha1.Role{
		Name:           "default", // Default name for the LWS role
		Replicas:       &roleReplicas,
		EntryTemplate:  convertTemplate(*lws.Spec.LeaderWorkerTemplate.LeaderTemplate),
		WorkerReplicas: workerReplicas,
		WorkerTemplate: convertTemplatePtr(&lws.Spec.LeaderWorkerTemplate.WorkerTemplate),
	}

	return &workloadv1alpha1.ModelServing{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lws.Name,
			Namespace: lws.Namespace,
		},
		Spec: workloadv1alpha1.ModelServingSpec{
			Replicas: &replicas,
			Template: workloadv1alpha1.ServingGroup{
				Roles: []workloadv1alpha1.Role{role},
			},
		},
	}
}

func (r *LWSReconciler) updateLWSStatus(ctx context.Context, lws *lwsv1.LeaderWorkerSet, ms *workloadv1alpha1.ModelServing) error {
	// Sync status from MS to LWS
	newStatus := lws.Status.DeepCopy()
	newStatus.Replicas = ms.Status.Replicas               // Total groups
	newStatus.ReadyReplicas = ms.Status.AvailableReplicas // Ready groups

	// TODO: Map Conditions properly if needed.
	// LWS has conditions like Available, Progressing. ModelServing also has Conditions.

	if !reflect.DeepEqual(lws.Status, *newStatus) {
		lws.Status = *newStatus
		return r.Status().Update(ctx, lws)
	}
	return nil
}

func (r *LWSReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lwsv1.LeaderWorkerSet{}).
		Owns(&workloadv1alpha1.ModelServing{}).
		Complete(r)
}
