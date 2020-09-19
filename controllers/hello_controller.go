/*


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

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	initv1alpha1 "github.com/sm43/hello-operator/api/v1alpha1"
)

// HelloReconciler reconciles a Hello object
type HelloReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=init.hello.com,resources=hellos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=init.hello.com,resources=hellos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;

func (r *HelloReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("hello", req.NamespacedName)

	// Fetch the Hello instance
	hello := &initv1alpha1.Hello{}
	err := r.Get(ctx, req.NamespacedName, hello)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			log.Info("Hello resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get Hello")
		return ctrl.Result{}, err
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: hello.Name, Namespace: hello.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentForHello(hello)
		log.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			log.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully

		svc := r.serviceForHello(hello)
		log.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		err = r.Create(ctx, svc)
		if err != nil {
			log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}
		// Service created successfully

		rt := r.routeForHello(hello)
		log.Info("Creating a new Route", "Route.Namespace", rt.Namespace, "Route.Name", rt.Name)
		err = r.Create(ctx, rt)
		if err != nil {
			log.Error(err, "Failed to create new Route", "Route.Namespace", rt.Namespace, "Route.Name", rt.Name)
			return ctrl.Result{}, err
		}
		// Route created successfully - return and requeue

		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	replicas := hello.Spec.Replica
	if *found.Spec.Replicas != replicas {
		found.Spec.Replicas = &replicas
		err = r.Update(ctx, found)
		if err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}
		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, nil
	}

	status := fmt.Sprintf("%s Hello CR running with %d replicas", found.Name, *found.Spec.Replicas)
	if status != hello.Status.Status {
		hello.Status.Status = status
		err = r.Status().Update(ctx, hello)
		if err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *HelloReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&initv1alpha1.Hello{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

// deploymentForHello returns a Hello Deployment object
func (r *HelloReconciler) deploymentForHello(h *initv1alpha1.Hello) *appsv1.Deployment {
	ls := labelsForHello(h.Name)
	replicas := h.Spec.Replica

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
			Labels:    ls,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: h.Spec.Image,
						Name:  "hello",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8080,
							Name:          "hello",
						}},
					}},
				},
			},
		},
	}
	// Set Hello instance as the owner and controller
	ctrl.SetControllerReference(h, dep, r.Scheme)
	return dep
}

// serviceForHello returns a Hello Service object
func (r HelloReconciler) serviceForHello(h *initv1alpha1.Hello) *corev1.Service {
	ls := labelsForHello(h.Name)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
			Labels:    ls,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: ls,
			Ports: []corev1.ServicePort{
				{
					Port: 8080,
					TargetPort: intstr.IntOrString{
						IntVal: 8080,
					},
				},
			},
		},
	}
	// Set Hello instance as the owner and controller
	ctrl.SetControllerReference(h, svc, r.Scheme)
	return svc
}

// routeForHello returns a Hello Route object
func (r HelloReconciler) routeForHello(h *initv1alpha1.Hello) *routev1.Route {
	ls := labelsForHello(h.Name)

	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.Name,
			Namespace: h.Namespace,
			Labels:    ls,
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: h.Name,
			},
			TLS: &routev1.TLSConfig{
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
				Termination:                   routev1.TLSTerminationEdge,
			},
		},
	}
	// Set Hello instance as the owner and controller
	ctrl.SetControllerReference(h, route, r.Scheme)
	return route
}

// labelsForHello returns the labels for selecting the resources
// belonging to the given hello CR name.
func labelsForHello(name string) map[string]string {
	return map[string]string{"app": "hello", "hello_cr": name}
}
