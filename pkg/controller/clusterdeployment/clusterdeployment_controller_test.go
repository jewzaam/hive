package clusterdeployment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/openpgp" //lint:ignore SA1019 only used in unit test

	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openshiftapiv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/library-go/pkg/verify"
	"github.com/openshift/library-go/pkg/verify/store"

	"github.com/openshift/hive/apis"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hivev1aws "github.com/openshift/hive/apis/hive/v1/aws"
	"github.com/openshift/hive/apis/hive/v1/azure"
	"github.com/openshift/hive/apis/hive/v1/baremetal"
	hiveintv1alpha1 "github.com/openshift/hive/apis/hiveinternal/v1alpha1"
	"github.com/openshift/hive/pkg/constants"
	controllerutils "github.com/openshift/hive/pkg/controller/utils"
	"github.com/openshift/hive/pkg/remoteclient"
	remoteclientmock "github.com/openshift/hive/pkg/remoteclient/mock"
	testassert "github.com/openshift/hive/pkg/test/assert"
	testclusterdeployment "github.com/openshift/hive/pkg/test/clusterdeployment"
	testclusterdeprovision "github.com/openshift/hive/pkg/test/clusterdeprovision"
	tcp "github.com/openshift/hive/pkg/test/clusterprovision"
	testdnszone "github.com/openshift/hive/pkg/test/dnszone"
)

const (
	testName                = "foo-lqmsh"
	testClusterName         = "bar"
	testClusterID           = "testFooClusterUUID"
	testInfraID             = "testFooInfraID"
	installConfigSecretName = "install-config-secret"
	provisionName           = "foo-lqmsh-random"
	imageSetJobName         = "foo-lqmsh-imageset"
	testNamespace           = "default"
	testSyncsetInstanceName = "testSSI"
	metadataName            = "foo-lqmsh-metadata"
	pullSecretSecret        = "pull-secret"
	installLogSecret        = "install-log-secret"
	globalPullSecret        = "global-pull-secret"
	adminKubeconfigSecret   = "foo-lqmsh-admin-kubeconfig"
	adminKubeconfig         = `clusters:
- cluster:
    certificate-authority-data: JUNK
    server: https://bar-api.clusters.example.com:6443
  name: bar
contexts:
- context:
    cluster: bar
  name: admin
current-context: admin
`
	adminPasswordSecret = "foo-lqmsh-admin-password"
	adminPassword       = "foo"
	credsSecret         = "foo-aws-creds"
	sshKeySecret        = "foo-ssh-key"

	remoteClusterRouteObjectName      = "console"
	remoteClusterRouteObjectNamespace = "openshift-console"
	testClusterImageSetName           = "test-image-set"
)

func init() {
	log.SetLevel(log.DebugLevel)
}

func fakeReadFile(content string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) {
		return []byte(content), nil
	}
}

func TestClusterDeploymentReconcile(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)
	openshiftapiv1.Install(scheme.Scheme)
	routev1.Install(scheme.Scheme)

	// Fake out readProvisionFailedConfig
	os.Setenv(constants.FailedProvisionConfigFileEnvVar, "fake")

	// Utility function to get the test CD from the fake client
	getCD := func(c client.Client) *hivev1.ClusterDeployment {
		cd := &hivev1.ClusterDeployment{}
		err := c.Get(context.TODO(), client.ObjectKey{Name: testName, Namespace: testNamespace}, cd)
		if err == nil {
			return cd
		}
		return nil
	}

	getCDC := func(c client.Client) *hivev1.ClusterDeploymentCustomization {
		cdc := &hivev1.ClusterDeploymentCustomization{}
		err := c.Get(context.TODO(), client.ObjectKey{Name: testName, Namespace: testNamespace}, cdc)
		if err == nil {
			return cdc
		}
		return nil
	}

	getDNSZone := func(c client.Client) *hivev1.DNSZone {
		zone := &hivev1.DNSZone{}
		err := c.Get(context.TODO(), client.ObjectKey{Name: testName + "-zone", Namespace: testNamespace}, zone)
		if err == nil {
			return zone
		}
		return nil
	}

	getDeprovision := func(c client.Client) *hivev1.ClusterDeprovision {
		req := &hivev1.ClusterDeprovision{}
		err := c.Get(context.TODO(), client.ObjectKey{Name: testName, Namespace: testNamespace}, req)
		if err == nil {
			return req
		}
		return nil
	}

	getImageSetJob := func(c client.Client) *batchv1.Job {
		return getJob(c, imageSetJobName)
	}

	imageVerifier := testReleaseVerifier{known: sets.NewString("sha256:digest1", "sha256:digest2", "sha256:digest3")}

	tests := []struct {
		name                          string
		existing                      []runtime.Object
		riVerifier                    verify.Interface
		pendingCreation               bool
		expectErr                     bool
		expectExplicitRequeue         bool
		expectedRequeueAfter          time.Duration
		expectPendingCreation         bool
		expectConsoleRouteFetch       bool
		validate                      func(client.Client, *testing.T)
		reconcilerSetup               func(*ReconcileClusterDeployment)
		platformCredentialsValidation func(client.Client, *hivev1.ClusterDeployment, log.FieldLogger) (bool, error)
		retryReasons                  *[]string
	}{
		{
			name: "Initialize conditions",
			existing: []runtime.Object{
				testClusterDeployment(),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				compareCD := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
				testassert.AssertConditions(t, cd, compareCD.Status.Conditions)
			},
		},
		{
			name: "Add finalizer",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithoutFinalizer()),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if cd == nil || !controllerutils.HasFinalizer(cd, hivev1.FinalizerDeprovision) {
					t.Errorf("did not get expected clusterdeployment finalizer")
				}
			},
		},
		{
			name: "Create provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				provisions := getProvisions(c)
				assert.Len(t, provisions, 1, "expected provision to exist")
				testassert.AssertConditions(t, getCD(c), []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonProvisioning,
					Message: "Cluster provision created",
				}})
			},
		},
		{
			name: "Provision not created when pending create",
			existing: []runtime.Object{
				testClusterDeployment(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			pendingCreation:       true,
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				provisions := getProvisions(c)
				assert.Empty(t, provisions, "expected provision to not exist")
				testassert.AssertConditions(t, getCD(c), []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionUnknown,
					Reason:  hivev1.InitializedConditionReason,
					Message: "Condition Initialized",
				}})
			},
		},
		{
			name: "Adopt provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithInitializedConditions(testClusterDeployment()),
				testProvision(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "no clusterdeployment found") {
					if assert.NotNil(t, cd.Status.ProvisionRef, "missing provision ref") {
						assert.Equal(t, provisionName, cd.Status.ProvisionRef.Name, "unexpected provision ref name")
					}
					// When adopting, we don't know the provisioning state until the *next* reconcile when we dig into the ClusterProvision.
					testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionUnknown,
						Reason:  hivev1.InitializedConditionReason,
						Message: "Condition Initialized",
					}})
				}
			},
		},
		{
			name: "Initializing provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())),
				testProvision(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "no clusterdeployment found") {
					e := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(
						testClusterDeploymentWithProvision()))
					e.Status.Conditions = addOrUpdateClusterDeploymentCondition(
						*e,
						hivev1.ProvisionedCondition,
						corev1.ConditionFalse,
						hivev1.ProvisionedReasonProvisioning,
						"Cluster provision initializing",
					)
					sanitizeConditions(e, cd)
					testassert.AssertEqualWhereItCounts(t, e, cd, "unexpected change in clusterdeployment")
				}
				provisions := getProvisions(c)
				if assert.Len(t, provisions, 1, "expected provision to exist") {
					e := testProvision()
					testassert.AssertEqualWhereItCounts(t, e, provisions[0], "unexpected change in provision")
				}
			},
		},
		{
			name: "Parse server URL from admin kubeconfig",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.Installed = true
					cd.Spec.ClusterMetadata = &hivev1.ClusterMetadata{
						InfraID:                  "fakeinfra",
						AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: adminKubeconfigSecret},
					}
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.UnreachableCondition,
						corev1.ConditionFalse, "test-reason", "test-message")
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testMetadataConfigMap(),
			},
			expectConsoleRouteFetch: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				assert.Equal(t, "https://bar-api.clusters.example.com:6443", cd.Status.APIURL)
				assert.Equal(t, "https://bar-api.clusters.example.com:6443/console", cd.Status.WebConsoleURL)
			},
		},
		{
			name: "Add additional CAs to admin kubeconfig",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.Installed = true
					cd.Spec.ClusterMetadata = &hivev1.ClusterMetadata{
						InfraID:                  "fakeinfra",
						AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: adminKubeconfigSecret},
					}
					cd.Status.WebConsoleURL = "https://example.com"
					cd.Status.APIURL = "https://example.com"
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testMetadataConfigMap(),
			},
			expectConsoleRouteFetch: false,
			validate: func(c client.Client, t *testing.T) {
				// Ensure the admin kubeconfig secret got a copy of the raw data, indicating that we would have
				// added additional CAs if any were configured.
				akcSecret := &corev1.Secret{}
				err := c.Get(context.TODO(), client.ObjectKey{Name: adminKubeconfigSecret, Namespace: testNamespace},
					akcSecret)
				require.NoError(t, err)
				require.NotNil(t, akcSecret)
				assert.Contains(t, akcSecret.Data, constants.RawKubeconfigSecretKey)
			},
		},
		{
			name: "Completed provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision()),
				testSuccessfulProvision(),
				testMetadataConfigMap(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					assert.True(t, cd.Spec.Installed, "expected cluster to be installed")
					assert.NotContains(t, cd.Annotations, constants.ProtectedDeleteAnnotation, "unexpected protected delete annotation")
					testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionTrue,
						Reason:  hivev1.ProvisionedReasonProvisioned,
						Message: "Cluster is provisioned",
					}})
				}
			},
		},
		{
			name: "Completed provision with protected delete",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision()),
				testSuccessfulProvision(),
				testMetadataConfigMap(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			reconcilerSetup: func(r *ReconcileClusterDeployment) {
				r.protectedDelete = true
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					assert.True(t, cd.Spec.Installed, "expected cluster to be installed")
					assert.Equal(t, "true", cd.Annotations[constants.ProtectedDeleteAnnotation], "unexpected protected delete annotation")
					testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionTrue,
						Reason:  hivev1.ProvisionedReasonProvisioned,
						Message: "Cluster is provisioned",
					}})
				}
			},
		},
		{
			name: "clusterdeployment must specify pull secret when there is no global pull secret ",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.PullSecretRef = nil
					return cd
				}(),
			},
			expectErr: true,
		},
		{
			name: "Legacy dockercfg pull secret causes no errors once installed",
			existing: []runtime.Object{
				testInstalledClusterDeployment(time.Date(2019, 9, 6, 11, 58, 32, 45, time.UTC)),
				testMetadataConfigMap(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeOpaque, adminPasswordSecret, "password", adminPassword),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
		},
		{
			name: "No-op deleted cluster without finalizer",
			existing: []runtime.Object{
				testDeletedClusterDeploymentWithoutFinalizer(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				deprovision := getDeprovision(c)
				if deprovision != nil {
					t.Errorf("got unexpected deprovision request")
				}
			},
		},
		{
			name: "Block deprovision when protected delete on",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeployment()
					if cd.Annotations == nil {
						cd.Annotations = make(map[string]string, 1)
					}
					cd.Annotations[constants.ProtectedDeleteAnnotation] = "true"
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(
						*cd,
						hivev1.ProvisionedCondition,
						corev1.ConditionTrue,
						hivev1.ProvisionedReasonProvisioned,
						"Cluster is provisioned",
					)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				deprovision := getDeprovision(c)
				assert.Nil(t, deprovision, "expected no deprovision request")
				cd := getCD(c)
				assert.Contains(t, cd.Finalizers, hivev1.FinalizerDeprovision, "expected finalizer")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionTrue,
					Reason:  hivev1.ProvisionedReasonProvisioned,
					Message: "Cluster is provisioned",
				}})
			},
		},
		{
			name: "Skip deprovision for deleted BareMetal cluster",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.Platform.AWS = nil
					cd.Spec.Platform.BareMetal = &baremetal.Platform{}
					cd.Labels[hivev1.HiveClusterPlatformLabel] = "baremetal"
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				deprovision := getDeprovision(c)
				assert.Nil(t, deprovision, "expected no deprovision request")
				cd := getCD(c)
				assert.Nil(t, cd, "expected ClusterDeployment to be deleted")
			},
		},
		{
			name: "Delete expired cluster deployment",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testExpiredClusterDeployment()),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				assert.NotNil(t, cd, "clusterdeployment deleted unexpectedly")
				assert.Contains(t, cd.Finalizers, hivev1.FinalizerDeprovision, "expected hive finalizer")
			},
		},
		{
			name: "Test PreserveOnDelete when cluster deployment is installed",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testDeletedClusterDeployment())
					cd.Spec.Installed = true
					cd.Spec.PreserveOnDelete = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				assert.Nil(t, cd, "expected clusterdeployment to be deleted")
				deprovision := getDeprovision(c)
				assert.Nil(t, deprovision, "expected no deprovision request")
			},
		},
		{
			name: "Test PreserveOnDelete when cluster deployment is not installed",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testDeletedClusterDeployment())
					cd.Spec.PreserveOnDelete = true
					cd.Spec.Installed = false
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				assert.Nil(t, cd, "expected clusterdeployment to be deleted")
				deprovision := getDeprovision(c)
				assert.Nil(t, deprovision, "expected no deprovision request")
			},
		},
		{
			name: "Create job to resolve installer image",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallerImage = nil
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					return cd
				}(),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				job := getImageSetJob(c)
				if job == nil {
					t.Errorf("did not find expected imageset job")
				}
				// Ensure that the release image from the imageset is used in the job
				//lint:ignore SA5011 never nil due to test setup
				envVars := job.Spec.Template.Spec.Containers[0].Env
				for _, e := range envVars {
					if e.Name == "RELEASE_IMAGE" {
						if e.Value != testClusterImageSet().Spec.ReleaseImage {
							t.Errorf("unexpected release image used in job: %s", e.Value)
						}
						break
					}
				}

				// Ensure job type labels are set correctly
				require.NotNil(t, job, "expected job")
				//lint:ignore SA5011 never nil due to test setup
				assert.Equal(t, testClusterDeployment().Name, job.Labels[constants.ClusterDeploymentNameLabel], "incorrect cluster deployment name label")
				//lint:ignore SA5011 never nil due to test setup
				assert.Equal(t, constants.JobTypeImageSet, job.Labels[constants.JobTypeLabel], "incorrect job type label")
			},
		},
		{
			name: "failed verification of release image using tags should set InstallImagesNotResolvedCondition",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallerImage = nil
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					return cd
				}(),
				testClusterImageSet(),
				testCompletedFailedImageSetJob(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			riVerifier: verify.Reject,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.InstallImagesNotResolvedCondition,
						Status: corev1.ConditionTrue,
						Reason: "ReleaseImageVerificationFailed",
					},
				})
			},
		},
		{
			name: "failed verification of release image using unknown digest should set InstallImagesNotResolvedCondition",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallerImage = nil
					cd.Spec.Provisioning.ReleaseImage = "test-image@sha256:unknowndigest1"
					return cd
				}(),
				testClusterImageSet(),
				testCompletedFailedImageSetJob(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			riVerifier: imageVerifier,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.InstallImagesNotResolvedCondition,
						Status: corev1.ConditionTrue,
						Reason: "ReleaseImageVerificationFailed",
					},
				})
			},
		},
		{
			name: "Create job to resolve installer image using verified image",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallerImage = nil
					cd.Spec.Provisioning.ReleaseImage = "test-image@sha256:digest1"
					return cd
				}(),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			riVerifier: imageVerifier,
			validate: func(c client.Client, t *testing.T) {
				job := getImageSetJob(c)
				if job == nil {
					t.Errorf("did not find expected imageset job")
				}
				// Ensure that the release image from the imageset is used in the job
				//lint:ignore SA5011 never nil due to test setup
				envVars := job.Spec.Template.Spec.Containers[0].Env
				for _, e := range envVars {
					if e.Name == "RELEASE_IMAGE" {
						if e.Value != "test-image@sha256:digest1" {
							t.Errorf("unexpected release image used in job: %s", e.Value)
						}
						break
					}
				}

				// Ensure job type labels are set correctly
				require.NotNil(t, job, "expected job")
				//lint:ignore SA5011 never nil due to test setup
				assert.Equal(t, testClusterDeployment().Name, job.Labels[constants.ClusterDeploymentNameLabel], "incorrect cluster deployment name label")
				//lint:ignore SA5011 never nil due to test setup
				assert.Equal(t, constants.JobTypeImageSet, job.Labels[constants.JobTypeLabel], "incorrect job type label")
			},
		},
		{
			name: "failed image should set InstallImagesNotResolved condition on clusterdeployment",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallerImage = nil
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					return cd
				}(),
				testClusterImageSet(),
				testCompletedFailedImageSetJob(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.InstallImagesNotResolvedCondition,
						Status: corev1.ConditionTrue,
						Reason: "JobToResolveImagesFailed",
					},
				})
			},
		},
		{
			name: "clear InstallImagesNotResolved condition on success",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Status.InstallerImage = pointer.StringPtr("test-installer-image")
					cd.Status.CLIImage = pointer.StringPtr("test-cli-image")
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.InstallImagesNotResolvedCondition,
						corev1.ConditionTrue, "test-reason", "test-message")
					return cd
				}(),
				testClusterImageSet(),
				testCompletedImageSetJob(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditionStatus(t, cd, hivev1.InstallImagesNotResolvedCondition, corev1.ConditionFalse)
			},
		},
		{
			name: "Delete imageset job when complete",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Status.InstallerImage = pointer.StringPtr("test-installer-image")
					cd.Status.CLIImage = pointer.StringPtr("test-cli-image")
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					return cd
				}(),
				testClusterImageSet(),
				testCompletedImageSetJob(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				job := getImageSetJob(c)
				assert.Nil(t, job, "expected imageset job to be deleted")
			},
		},
		{
			name: "Ensure release image from clusterdeployment (when present) is used to generate imageset job",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallerImage = nil
					cd.Spec.Provisioning.ReleaseImage = "embedded-release-image:latest"
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					return cd
				}(),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				job := getImageSetJob(c)
				if job == nil {
					t.Errorf("did not find expected imageset job")
				}
				//lint:ignore SA5011 never nil due to test setup
				envVars := job.Spec.Template.Spec.Containers[0].Env
				for _, e := range envVars {
					if e.Name == "RELEASE_IMAGE" {
						if e.Value != "embedded-release-image:latest" {
							t.Errorf("unexpected release image used in job: %s", e.Value)
						}
						break
					}
				}
			},
		},
		{
			name: "Ensure release image from clusterimageset is used as override image in install job",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Status.InstallerImage = pointer.StringPtr("test-installer-image:latest")
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					return cd
				}(),
				func() *hivev1.ClusterImageSet {
					cis := testClusterImageSet()
					cis.Spec.ReleaseImage = "test-release-image:latest"
					return cis
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				provisions := getProvisions(c)
				if assert.Len(t, provisions, 1, "expected provision to exist") {
					env := provisions[0].Spec.PodSpec.Containers[0].Env
					variable := corev1.EnvVar{}
					found := false
					for _, e := range env {
						if e.Name == "OPENSHIFT_INSTALL_RELEASE_IMAGE_OVERRIDE" {
							variable = e
							found = true
							break
						}
					}
					if !found {
						t.Errorf("did not find expected override environment variable in job")
						return
					}
					if variable.Value != "test-release-image:latest" {
						t.Errorf("environment variable did not have the expected value. actual: %s", variable.Value)
					}
				}
			},
		},
		{
			name: "Create DNSZone when manageDNS is true",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Spec.PreserveOnDelete = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				zone := getDNSZone(c)
				require.NotNil(t, zone, "dns zone should exist")
				assert.Equal(t, testClusterDeployment().Name, zone.Labels[constants.ClusterDeploymentNameLabel], "incorrect cluster deployment name label")
				assert.Equal(t, constants.DNSZoneTypeChild, zone.Labels[constants.DNSZoneTypeLabel], "incorrect dnszone type label")
				assert.True(t, zone.Spec.PreserveOnDelete, "PreserveOnDelete did not transfer to DNSZone")
			},
		},
		{
			name: "Create DNSZone with Azure CloudName",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					baseCD := testClusterDeployment()
					baseCD.Labels[hivev1.HiveClusterPlatformLabel] = "azure"
					baseCD.Labels[hivev1.HiveClusterRegionLabel] = "usgovvirginia"
					baseCD.Spec.Platform.AWS = nil
					baseCD.Spec.Platform.Azure = &azure.Platform{
						CredentialsSecretRef: corev1.LocalObjectReference{
							Name: "azure-credentials",
						},
						Region:                      "usgovvirginia",
						CloudName:                   azure.USGovernmentCloud,
						BaseDomainResourceGroupName: "os4-common",
					}
					baseCD.Spec.ManageDNS = true
					baseCD.Spec.PreserveOnDelete = true
					return testClusterDeploymentWithInitializedConditions(baseCD)
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				zone := getDNSZone(c)
				require.NotNil(t, zone, "dns zone should exist")
				assert.Equal(t, azure.USGovernmentCloud, zone.Spec.Azure.CloudName, "CloudName did not transfer to DNSZone")
			},
		},
		{
			name: "Create DNSZone without Azure CloudName",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					baseCD := testClusterDeployment()
					baseCD.Labels[hivev1.HiveClusterPlatformLabel] = "azure"
					baseCD.Labels[hivev1.HiveClusterRegionLabel] = "eastus"
					baseCD.Spec.Platform.AWS = nil
					baseCD.Spec.Platform.Azure = &azure.Platform{
						CredentialsSecretRef: corev1.LocalObjectReference{
							Name: "azure-credentials",
						},
						Region:                      "eastus",
						BaseDomainResourceGroupName: "os4-common",
					}
					baseCD.Spec.ManageDNS = true
					baseCD.Spec.PreserveOnDelete = true
					return testClusterDeploymentWithInitializedConditions(baseCD)
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				zone := getDNSZone(c)
				require.NotNil(t, zone, "dns zone should exist")
				assert.Equal(t, azure.CloudEnvironment(""), zone.Spec.Azure.CloudName, "CloudName incorrectly set for DNSZone")
			},
		},
		{
			name: "Update DNSZone when PreserveOnDelete changes",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Spec.PreserveOnDelete = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZone(),
			},
			validate: func(c client.Client, t *testing.T) {
				zone := getDNSZone(c)
				require.NotNil(t, zone, "dns zone should exist")
				assert.True(t, zone.Spec.PreserveOnDelete, "PreserveOnDelete was not updated")
			},
		},
		{
			name: "Update DNSZone when PreserveOnDelete changes on deleted cluster",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Spec.PreserveOnDelete = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZoneWithFinalizer(),
			},
			validate: func(c client.Client, t *testing.T) {
				zone := getDNSZone(c)
				require.NotNil(t, zone, "dns zone should exist")
				assert.True(t, zone.Spec.PreserveOnDelete, "PreserveOnDelete was not updated")
			},
		},
		{
			name: "Update DNSZone when PreserveOnDelete changes for installed cluster",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testInstalledClusterDeployment(time.Now()))
					cd.Spec.ManageDNS = true
					cd.Spec.PreserveOnDelete = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testAvailableDNSZone(),
			},
			validate: func(c client.Client, t *testing.T) {
				zone := getDNSZone(c)
				require.NotNil(t, zone, "dns zone should exist")
				assert.True(t, zone.Spec.PreserveOnDelete, "PreserveOnDelete was not updated")
			},
		},
		{
			name: "Update DNSZone when PreserveOnDelete changes for installed deleted cluster",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testInstalledClusterDeployment(time.Now()))
					cd.Spec.ManageDNS = true
					cd.Spec.PreserveOnDelete = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testAvailableDNSZone(),
			},
			validate: func(c client.Client, t *testing.T) {
				zone := getDNSZone(c)
				require.NotNil(t, zone, "dns zone should exist")
				assert.True(t, zone.Spec.PreserveOnDelete, "PreserveOnDelete was not updated")
			},
		},
		{
			name: "DNSZone is not available yet",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZone(),
			},
			expectExplicitRequeue: true,
			expectedRequeueAfter:  defaultDNSNotReadyTimeout,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.DNSNotReadyCondition,
						Status: corev1.ConditionTrue,
						Reason: dnsNotReadyReason,
					},
				})
				provisions := getProvisions(c)
				assert.Empty(t, provisions, "provision should not exist")
			},
		},
		{
			name: "DNSZone is not available: requeue time",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					// Pretend DNSNotReady happened 4m ago
					cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.DNSNotReadyCondition)
					cond.Status = corev1.ConditionTrue
					cond.Reason = dnsNotReadyReason
					cond.Message = "DNS Zone not yet available"
					cond.LastTransitionTime.Time = time.Now().Add(-4 * time.Minute)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZone(),
			},
			expectExplicitRequeue: true,
			expectedRequeueAfter:  defaultDNSNotReadyTimeout - (4 * time.Minute),
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.DNSNotReadyCondition,
						Status: corev1.ConditionTrue,
						Reason: dnsNotReadyReason,
					},
				})
				provisions := getProvisions(c)
				assert.Empty(t, provisions, "provision should not exist")
			},
		},
		{
			name: "DNSZone timeout",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					// Pretend DNSNotReady happened >10m ago
					cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.DNSNotReadyCondition)
					cond.Status = corev1.ConditionTrue
					cond.Reason = dnsNotReadyReason
					cond.Message = "DNS Zone not yet available"
					cond.LastTransitionTime.Time = time.Now().Add(-11 * time.Minute)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZone(),
			},
			expectExplicitRequeue: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					// DNS timed out
					{
						Type:   hivev1.DNSNotReadyCondition,
						Status: corev1.ConditionTrue,
						Reason: dnsNotReadyTimedoutReason,
					},
					// Not Provisioned
					{
						Type:   hivev1.ProvisionedCondition,
						Status: corev1.ConditionFalse,
						Reason: hivev1.ProvisionedReasonProvisionStopped,
					},
					// Provision stopped
					{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
						Reason: dnsNotReadyTimedoutReason,
					},
				})
				provisions := getProvisions(c)
				assert.Empty(t, provisions, "provision should not exist")
			},
		},
		{
			name: "Set condition when DNSZone cannot be created due to credentials missing permissions",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZoneWithInvalidCredentialsCondition(),
			},
			expectExplicitRequeue: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.DNSNotReadyCondition,
						Status: corev1.ConditionTrue,
						Reason: "InsufficientCredentials",
					},
				})
			},
		},
		{
			name: "Set condition when DNSZone cannot be created due to api opt-in required for DNS apis",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZoneWithAPIOptInRequiredCondition(),
			},
			expectExplicitRequeue: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.DNSNotReadyCondition,
						Status: corev1.ConditionTrue,
						Reason: "APIOptInRequiredForDNS",
					},
				})
			},
		},
		{
			name: "Set condition when DNSZone cannot be created due to authentication failure",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZoneWithAuthenticationFailureCondition(),
			},
			expectExplicitRequeue: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.DNSNotReadyCondition,
						Status: corev1.ConditionTrue,
						Reason: "AuthenticationFailure",
					},
				})
			},
		},
		{
			name: "Set condition when DNSZone cannot be created due to a cloud error",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZoneWithDNSErrorCondition(),
			},
			expectExplicitRequeue: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:    hivev1.DNSNotReadyCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "CloudError",
						Message: "Some cloud error occurred",
					},
				})
			},
		},
		{
			name: "Clear condition when DNSZone is available",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Status.Conditions = controllerutils.SetClusterDeploymentCondition(
						cd.Status.Conditions,
						hivev1.DNSNotReadyCondition,
						corev1.ConditionTrue,
						"no reason",
						"no message",
						controllerutils.UpdateConditionIfReasonOrMessageChange)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testAvailableDNSZone(),
			},
			expectExplicitRequeue: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditionStatus(t, cd, hivev1.DNSNotReadyCondition, corev1.ConditionFalse)
			},
		},
		{
			name: "Do not use unowned DNSZone",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				func() *hivev1.DNSZone {
					zone := testDNSZone()
					zone.OwnerReferences = []metav1.OwnerReference{}
					return zone
				}(),
			},
			expectErr: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.DNSNotReadyCondition)
					if assert.NotNil(t, cond, "expected to find condition") {
						assert.Equal(t, corev1.ConditionTrue, cond.Status, "unexpected condition status")
						assert.Equal(t, "Existing DNS zone not owned by cluster deployment", cond.Message, "unexpected condition message")
					}
				}
				zone := getDNSZone(c)
				assert.NotNil(t, zone, "expected DNSZone to exist")
			},
		},
		{
			name: "Do not use DNSZone owned by other clusterdeployment",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				func() *hivev1.DNSZone {
					zone := testDNSZone()
					zone.OwnerReferences[0].UID = "other-uid"
					return zone
				}(),
			},
			expectErr: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.DNSNotReadyCondition)
					if assert.NotNil(t, cond, "expected to find condition") {
						assert.Equal(t, corev1.ConditionTrue, cond.Status, "unexpected condition status")
						assert.Equal(t, "Existing DNS zone not owned by cluster deployment", cond.Message, "unexpected condition message")
					}
				}
				zone := getDNSZone(c)
				assert.NotNil(t, zone, "expected DNSZone to exist")
			},
		},
		{
			name: "Create provision when DNSZone is ready",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithDefaultConditions(
						testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Spec.ManageDNS = true
					cd.Annotations = map[string]string{dnsReadyAnnotation: "NOW"}
					controllerutils.SetClusterDeploymentCondition(
						cd.Status.Conditions,
						hivev1.DNSNotReadyCondition,
						corev1.ConditionFalse,
						dnsReadyReason,
						"DNS Zone available",
						controllerutils.UpdateConditionAlways,
					)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testAvailableDNSZone(),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				provisions := getProvisions(c)
				assert.Len(t, provisions, 1, "expected provision to exist")
				testassert.AssertConditions(t, getCD(c), []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonProvisioning,
					Message: "Cluster provision created",
				}})
			},
		},
		{
			name: "Set DNS delay metric",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testAvailableDNSZone(),
			},
			expectExplicitRequeue: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				assert.NotNil(t, cd.Annotations, "annotations should be set on clusterdeployment")
				assert.Contains(t, cd.Annotations, dnsReadyAnnotation)
			},
		},
		{
			name: "Ensure managed DNSZone is deleted with cluster deployment",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testDeletedClusterDeployment())
					cd.Spec.ManageDNS = true
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testDNSZone(),
			},
			validate: func(c client.Client, t *testing.T) {
				dnsZone := getDNSZone(c)
				assert.Nil(t, dnsZone, "dnsZone should not exist")
			},
		},
		{
			name: "Delete cluster deployment with missing clusterimageset",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testDeletedClusterDeployment())
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				deprovision := getDeprovision(c)
				assert.NotNil(t, deprovision, "expected deprovision request to be created")
			},
		},
		{
			name: "Delete old provisions",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Status.InstallRestarts = 4
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testProvision(tcp.Failed(), tcp.Attempt(0)),
				testProvision(tcp.Failed(), tcp.Attempt(1)),
				testProvision(tcp.Failed(), tcp.Attempt(2)),
				testProvision(tcp.Failed(), tcp.Attempt(3)),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				actualAttempts := []int{}
				for _, p := range getProvisions(c) {
					actualAttempts = append(actualAttempts, p.Spec.Attempt)
				}
				expectedAttempts := []int{0, 2, 3, 4}
				assert.ElementsMatch(t, expectedAttempts, actualAttempts, "unexpected provisions kept")
			},
		},
		{
			name: "Do not adopt failed provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testProvision(tcp.Failed(), tcp.Attempt(0)),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing cluster deployment") {
					assert.Nil(t, cd.Status.ProvisionRef, "expected provision reference to not be set")
					// This code path creates a new provision, but it doesn't get adopted until the *next* reconcile
					testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionFalse,
						Reason:  hivev1.ProvisionedReasonProvisioning,
						Message: "Cluster provision created",
					}})
				}
			},
		},
		{
			name: "Delete-after requeue",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())
					cd.CreationTimestamp = metav1.Now()
					if cd.Annotations == nil {
						cd.Annotations = make(map[string]string, 1)
					}
					cd.Annotations[deleteAfterAnnotation] = "8h"
					return cd
				}(),
				testProvision(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectExplicitRequeue: true,
			expectedRequeueAfter:  8 * time.Hour,
		},
		{
			name: "Wait after failed provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())
					cd.CreationTimestamp = metav1.Now()
					if cd.Annotations == nil {
						cd.Annotations = make(map[string]string, 1)
					}
					cd.Annotations[deleteAfterAnnotation] = "8h"
					return cd
				}(),
				testProvision(tcp.WithFailureTime(time.Now())),
				testMetadataConfigMap(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectedRequeueAfter: 1 * time.Minute,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					if assert.NotNil(t, cd.Status.ProvisionRef, "missing provision ref") {
						assert.Equal(t, provisionName, cd.Status.ProvisionRef.Name, "unexpected provision ref name")
					}
					testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionUnknown,
						Reason:  hivev1.InitializedConditionReason,
						Message: "Condition Initialized",
					}})
				}
			},
		},
		{
			name: "Clear out provision after wait time",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision()),
				testProvision(tcp.WithFailureTime(time.Now().Add(-2 * time.Minute))),
				testMetadataConfigMap(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					assert.Nil(t, cd.Status.ProvisionRef, "expected empty provision ref")
					assert.Equal(t, 1, cd.Status.InstallRestarts, "expected incremented install restart count")
				}
			},
		},
		{
			name: "Delete outstanding provision on delete",
			existing: []runtime.Object{
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(
						*cd,
						hivev1.ProvisionedCondition,
						corev1.ConditionTrue,
						hivev1.ProvisionedReasonProvisioned,
						"Cluster is provisioned",
					)
					return cd
				}(),
				testProvision(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectedRequeueAfter: defaultRequeueTime,
			validate: func(c client.Client, t *testing.T) {
				provisions := getProvisions(c)
				assert.Empty(t, provisions, "expected provision to be deleted")
				deprovision := getDeprovision(c)
				assert.Nil(t, deprovision, "expect not to create deprovision request until provision removed")
				testassert.AssertConditions(t, getCD(c), []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionTrue,
					Reason:  hivev1.ProvisionedReasonProvisioned,
					Message: "Cluster is provisioned",
				}})
			},
		},
		{
			name: "Remove finalizer after early-failure provision removed",
			existing: []runtime.Object{
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					cd.Spec.ClusterMetadata = nil
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				assert.Nil(t, cd, "expected clusterdeployment to be deleted")
			},
		},
		{
			name: "Create deprovision after late-failure provision removed",
			existing: []runtime.Object{
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					assert.Contains(t, cd.Finalizers, hivev1.FinalizerDeprovision, "expected hive finalizer")
				}
				deprovision := getDeprovision(c)
				assert.NotNil(t, deprovision, "missing deprovision request")
				testassert.AssertConditions(t, getCD(c), []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonDeprovisioning,
					Message: "Cluster is being deprovisioned",
				}})
			},
		},
		{
			name: "SyncSetFailedCondition should be present",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testInstalledClusterDeployment(time.Now())),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeOpaque, adminPasswordSecret, "password", adminPassword),
				&hiveintv1alpha1.ClusterSync{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testNamespace,
						Name:      testName,
					},
					Status: hiveintv1alpha1.ClusterSyncStatus{
						Conditions: []hiveintv1alpha1.ClusterSyncCondition{{
							Type:    hiveintv1alpha1.ClusterSyncFailed,
							Status:  corev1.ConditionTrue,
							Reason:  "FailureReason",
							Message: "Failure message",
						}},
					},
				},
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.SyncSetFailedCondition)
					if assert.NotNil(t, cond, "missing SyncSetFailedCondition status condition") {
						assert.Equal(t, corev1.ConditionTrue, cond.Status, "did not get expected state for SyncSetFailedCondition condition")
					}
				}
			},
		},
		{
			name: "SyncSetFailedCondition value should be corev1.ConditionFalse",
			existing: []runtime.Object{
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testInstalledClusterDeployment(time.Now()))
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd,
						hivev1.SyncSetFailedCondition,
						corev1.ConditionTrue,
						"test reason",
						"test message")
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeOpaque, adminPasswordSecret, "password", adminPassword),
				&hiveintv1alpha1.ClusterSync{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testNamespace,
						Name:      testName,
					},
					Status: hiveintv1alpha1.ClusterSyncStatus{
						Conditions: []hiveintv1alpha1.ClusterSyncCondition{{
							Type:    hiveintv1alpha1.ClusterSyncFailed,
							Status:  corev1.ConditionFalse,
							Reason:  "SuccessReason",
							Message: "Success message",
						}},
					},
				},
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.SyncSetFailedCondition)
				if assert.NotNil(t, cond, "missing SyncSetFailedCondition status condition") {
					assert.Equal(t, corev1.ConditionFalse, cond.Status, "did not get expected state for SyncSetFailedCondition condition")
				}
			},
		},
		{
			name: "SyncSet is Paused and ClusterSync object is missing",
			existing: []runtime.Object{
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testInstalledClusterDeployment(time.Now()))
					if cd.Annotations == nil {
						cd.Annotations = make(map[string]string, 1)
					}
					cd.Annotations[constants.SyncsetPauseAnnotation] = "true"
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeOpaque, adminPasswordSecret, "password", adminPassword),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.SyncSetFailedCondition)
				if assert.NotNil(t, cond, "missing SyncSetFailedCondition status condition") {
					assert.Equal(t, corev1.ConditionTrue, cond.Status, "did not get expected state for SyncSetFailedCondition condition")
					assert.Equal(t, "SyncSetPaused", cond.Reason, "did not get expected reason for SyncSetFailedCondition condition")
				}
			},
		},
		{
			name: "Add cluster platform label",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithoutPlatformLabel()),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					assert.Equal(t, getClusterPlatform(cd), cd.Labels[hivev1.HiveClusterPlatformLabel], "incorrect cluster platform label")
				}
			},
		},
		{
			name: "Add cluster region label",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithoutRegionLabel()),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					assert.Equal(t, getClusterRegion(cd), cd.Labels[hivev1.HiveClusterRegionLabel], "incorrect cluster region label")
					assert.Equal(t, getClusterRegion(cd), "us-east-1", "incorrect cluster region label")
				}
			},
		},
		{
			name: "Ensure cluster metadata set from provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())
					cd.Spec.ClusterMetadata = nil
					return cd
				}(),
				testSuccessfulProvision(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					if assert.NotNil(t, cd.Spec.ClusterMetadata, "expected cluster metadata to be set") {
						assert.Equal(t, testInfraID, cd.Spec.ClusterMetadata.InfraID, "unexpected infra ID")
						assert.Equal(t, testClusterID, cd.Spec.ClusterMetadata.ClusterID, "unexpected cluster ID")
						assert.Equal(t, adminKubeconfigSecret, cd.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name, "unexpected admin kubeconfig")
						assert.Equal(t, adminPasswordSecret, cd.Spec.ClusterMetadata.AdminPasswordSecretRef.Name, "unexpected admin password")
					}
				}
			},
		},
		{
			name: "Ensure cluster metadata overwrites from provision",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision())
					cd.Spec.ClusterMetadata = &hivev1.ClusterMetadata{
						InfraID:                  "old-infra-id",
						ClusterID:                "old-cluster-id",
						AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: "old-kubeconfig-secret"},
						AdminPasswordSecretRef:   &corev1.LocalObjectReference{Name: "old-password-secret"},
					}
					return cd
				}(),
				testSuccessfulProvision(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				if assert.NotNil(t, cd, "missing clusterdeployment") {
					if assert.NotNil(t, cd.Spec.ClusterMetadata, "expected cluster metadata to be set") {
						assert.Equal(t, testInfraID, cd.Spec.ClusterMetadata.InfraID, "unexpected infra ID")
						assert.Equal(t, testClusterID, cd.Spec.ClusterMetadata.ClusterID, "unexpected cluster ID")
						assert.Equal(t, adminKubeconfigSecret, cd.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name, "unexpected admin kubeconfig")
						assert.Equal(t, adminPasswordSecret, cd.Spec.ClusterMetadata.AdminPasswordSecretRef.Name, "unexpected admin password")
					}
				}
			},
		},
		{
			name: "set ClusterImageSet missing condition",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: "doesntexist"}
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectErr: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.RequirementsMetCondition,
						Status: corev1.ConditionFalse,
						Reason: clusterImageSetNotFoundReason,
					},
				})
			},
		},
		{
			name: "do not set ClusterImageSet missing condition for installed cluster",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(
						testInstalledClusterDeployment(time.Now()))
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: "doesntexist"}
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectErr: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.RequirementsMetCondition,
						Status: corev1.ConditionUnknown,
						Reason: "Initialized",
					},
				})
			},
		},
		{
			name: "clear ClusterImageSet missing condition",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.RequirementsMetCondition,
						corev1.ConditionFalse, clusterImageSetNotFoundReason, "test-message")
					return cd
				}(),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.RequirementsMetCondition,
						Status: corev1.ConditionTrue,
						Reason: "AllRequirementsMet",
					},
				})
			},
		},
		{
			name: "clear legacy conditions",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testInstalledClusterDeployment(time.Now())))
					cd.Spec.Provisioning.ImageSetRef = &hivev1.ClusterImageSetReference{Name: testClusterImageSetName}
					cd.Status.Conditions = append(cd.Status.Conditions,
						hivev1.ClusterDeploymentCondition{
							Type:    hivev1.ClusterImageSetNotFoundCondition,
							Status:  corev1.ConditionFalse,
							Reason:  clusterImageSetFoundReason,
							Message: "test-message",
						})
					return cd
				}(),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				lc := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ClusterImageSetNotFoundCondition)
				assert.Nil(t, lc, "legacy condition was not cleared")
			},
		},
		{
			name: "Add ownership to admin secrets",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.Installed = true
					cd.Spec.ClusterMetadata = &hivev1.ClusterMetadata{
						InfraID:                  "fakeinfra",
						AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: adminKubeconfigSecret},
						AdminPasswordSecretRef:   &corev1.LocalObjectReference{Name: adminPasswordSecret},
					}
					cd.Status.WebConsoleURL = "https://example.com"
					cd.Status.APIURL = "https://example.com"
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeOpaque, adminPasswordSecret, "password", adminPassword),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(
					testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
				testMetadataConfigMap(),
			},
			validate: func(c client.Client, t *testing.T) {
				secretNames := []string{adminKubeconfigSecret, adminPasswordSecret}
				for _, secretName := range secretNames {
					secret := &corev1.Secret{}
					err := c.Get(context.TODO(), client.ObjectKey{Name: secretName, Namespace: testNamespace},
						secret)
					require.NoErrorf(t, err, "not found secret %s", secretName)
					require.NotNilf(t, secret, "expected secret %s", secretName)
					assert.Equalf(t, testClusterDeployment().Name, secret.Labels[constants.ClusterDeploymentNameLabel],
						"incorrect cluster deployment name label for %s", secretName)
					refs := secret.GetOwnerReferences()
					cdAsOwnerRef := false
					for _, ref := range refs {
						if ref.Name == testName {
							cdAsOwnerRef = true
						}
					}
					assert.Truef(t, cdAsOwnerRef, "cluster deployment not owner of %s", secretName)
				}
			},
		},
		{
			name: "delete finalizer when deprovision complete and dnszone gone",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Spec.Installed = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
					testclusterdeprovision.Completed(),
				),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.Nil(t, cd, "expected ClusterDeployment to be deleted")
			},
		},
		{
			name: "release customization on deprovision",
			existing: []runtime.Object{
				testClusterDeploymentCustomization(testName),
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.Installed = true
					cd.Spec.ClusterPoolRef = &hivev1.ClusterPoolReference{
						Namespace:        testNamespace,
						CustomizationRef: &corev1.LocalObjectReference{Name: testName},
					}
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
					testclusterdeprovision.Completed(),
				),
			},
			validate: func(c client.Client, t *testing.T) {
				testassert.AssertCDCConditions(t, getCDC(c), []conditionsv1.Condition{{
					Type:    conditionsv1.ConditionAvailable,
					Status:  corev1.ConditionTrue,
					Reason:  "Available",
					Message: "available",
				}})
			},
		},
		{
			name: "deprovision finished",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Spec.Installed = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					cd.Finalizers = append(cd.Finalizers, "prevent the CD from being deleted so we can validate deprovisioned status")
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
					testclusterdeprovision.Completed(),
				),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonDeprovisioned,
					Message: "Cluster is deprovisioned",
				}})
			},
		},
		{
			name: "existing deprovision in progress",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Spec.Installed = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
				),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonDeprovisioning,
					Message: "Cluster is deprovisioning",
				}})
			},
		},
		{
			name: "deprovision failed",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ManageDNS = true
					cd.Spec.Installed = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
					testclusterdeprovision.WithAuthenticationFailure(),
				),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonDeprovisionFailed,
					Message: "Cluster deprovision failed",
				}})
			},
		},
		{
			name: "wait for deprovision to complete",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeployment()
					cd.Spec.ManageDNS = true
					cd.Spec.Installed = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(
						*cd,
						hivev1.ProvisionedCondition,
						corev1.ConditionFalse,
						hivev1.ProvisionedReasonDeprovisioning,
						"Cluster is being deprovisioned",
					)
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
				),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				assert.Contains(t, cd.Finalizers, hivev1.FinalizerDeprovision, "expected finalizer not to be removed from ClusterDeployment")
				testassert.AssertConditions(t, getCD(c), []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonDeprovisioning,
					Message: "Cluster is being deprovisioned",
				}})
			},
		},
		{
			name: "wait for dnszone to be gone",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeployment()
					cd.Spec.ManageDNS = true
					cd.Spec.Installed = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
					testclusterdeprovision.Completed(),
				),
				func() *hivev1.DNSZone {
					dnsZone := testDNSZone()
					now := metav1.Now()
					dnsZone.DeletionTimestamp = &now
					return dnsZone
				}(),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				assert.Contains(t, cd.Finalizers, hivev1.FinalizerDeprovision, "expected finalizer not to be removed from ClusterDeployment")
			},
			expectedRequeueAfter: defaultRequeueTime,
		},
		{
			name: "do not wait for dnszone to be gone when not using managed dns",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.Installed = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					return cd
				}(),
				testclusterdeprovision.Build(
					testclusterdeprovision.WithNamespace(testNamespace),
					testclusterdeprovision.WithName(testName),
					testclusterdeprovision.Completed(),
				),
				func() *hivev1.DNSZone {
					dnsZone := testDNSZone()
					now := metav1.Now()
					dnsZone.DeletionTimestamp = &now
					return dnsZone
				}(),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.Nil(t, cd, "expected ClusterDeployment to be deleted")
			},
		},
		{
			name: "wait for dnszone to be gone when install failed early",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeployment()
					cd.Spec.ManageDNS = true
					now := metav1.Now()
					cd.DeletionTimestamp = &now
					cd.Spec.ClusterMetadata = nil
					return cd
				}(),
				func() *hivev1.DNSZone {
					dnsZone := testDNSZone()
					now := metav1.Now()
					dnsZone.DeletionTimestamp = &now
					return dnsZone
				}(),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				assert.Contains(t, cd.Finalizers, hivev1.FinalizerDeprovision, "expected finalizer not to be removed from ClusterDeployment")
			},
			expectedRequeueAfter: defaultRequeueTime,
		},
		{
			name: "set InstallLaunchErrorCondition when install pod is stuck in pending phase",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				testClusterDeploymentWithInitializedConditions(testClusterDeploymentWithProvision()),
				testProvision(tcp.WithStuckInstallPod()),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.InstallLaunchErrorCondition,
						Status: corev1.ConditionTrue,
						Reason: "PodInPendingPhase",
					},
					// This is not terminal; to the outside world, the CD is still provisioning
					{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionFalse,
						Reason:  hivev1.ProvisionedReasonProvisioning,
						Message: "Cluster provision initializing",
					},
				})
			},
		},
		{
			name: "install attempts is less than the limit",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Status.InstallRestarts = 1
					cd.Spec.InstallAttemptsLimit = pointer.Int32Ptr(2)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ProvisionStoppedCondition, corev1.ConditionFalse)
				testassert.AssertConditions(t, getCD(c), []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionFalse,
					Reason:  hivev1.ProvisionedReasonProvisioning,
					Message: "Cluster provision created",
				}})
			},
		},
		{
			name: "ProvisionStopped already",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.Conditions = append(cd.Status.Conditions, hivev1.ClusterDeploymentCondition{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
					})
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
					},
				})
			},
		},
		{
			name: "install attempts is equal to the limit",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallRestarts = 2
					cd.Spec.InstallAttemptsLimit = pointer.Int32Ptr(2)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
						Reason: "InstallAttemptsLimitReached",
					},
					{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionFalse,
						Reason:  hivev1.ProvisionedReasonProvisionStopped,
						Message: "Provisioning failed terminally (see the ProvisionStopped condition for details)",
					},
				})
			},
		},
		{
			name: "install attempts is greater than the limit",
			existing: []runtime.Object{
				testInstallConfigSecret(),
				func() runtime.Object {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Status.InstallRestarts = 3
					cd.Spec.InstallAttemptsLimit = pointer.Int32Ptr(2)
					return cd
				}(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
						Reason: "InstallAttemptsLimitReached",
					},
					{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionFalse,
						Reason:  hivev1.ProvisionedReasonProvisionStopped,
						Message: "Provisioning failed terminally (see the ProvisionStopped condition for details)",
					},
				})
			},
		},
		{
			name: "auth condition when platform creds are bad",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterDeployment()),
			},
			platformCredentialsValidation: func(client.Client, *hivev1.ClusterDeployment, log.FieldLogger) (bool, error) {
				return false, errors.New("Post \"https://xxx.xxx.xxx.xxx/sdk\": x509: cannot validate certificate for xxx.xxx.xxx.xxx because it doesn't contain any IP SANs")
			},
			expectErr: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")

				testassert.AssertConditionStatus(t, cd, hivev1.AuthenticationFailureClusterDeploymentCondition, corev1.ConditionTrue)
				// Preflight check happens before we declare provisioning
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionUnknown,
						Reason:  hivev1.InitializedConditionReason,
						Message: "Condition Initialized",
					},
					{
						Type:    hivev1.AuthenticationFailureClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  platformAuthFailureReason,
						Message: "Platform credentials failed authentication check: Post \"https://xxx.xxx.xxx.xxx/sdk\": x509: cannot validate certificate for xxx.xxx.xxx.xxx because it doesn't contain any IP SANs",
					},
				})
			},
		},
		{
			name: "no ClusterProvision when platform creds are bad",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					return cd
				}(),
			},
			platformCredentialsValidation: func(client.Client, *hivev1.ClusterDeployment, log.FieldLogger) (bool, error) {
				return false, nil
			},
			expectErr: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				// Preflight check happens before we declare provisioning
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionUnknown,
					Reason:  hivev1.InitializedConditionReason,
					Message: "Condition Initialized",
				}})

				provisionList := &hivev1.ClusterProvisionList{}
				err := c.List(context.TODO(), provisionList, client.InNamespace(cd.Namespace))
				require.NoError(t, err, "unexpected error listing ClusterProvisions")

				assert.Zero(t, len(provisionList.Items), "expected no ClusterProvision objects when platform creds are bad")
			},
		},
		{
			name: "clusterinstallref not found",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")),
			},
			expectErr: true,
		},
		{
			name: "clusterinstallref exists, but no imagesetref",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")),
				testFakeClusterInstall("test-fake"),
			},
			expectErr: true,
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")

				testassert.AssertConditionStatus(t, cd, hivev1.RequirementsMetCondition, corev1.ConditionFalse)
				// Preflight check happens before we declare provisioning
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionUnknown,
					Reason:  hivev1.InitializedConditionReason,
					Message: "Condition Initialized",
				}})
			},
		},
		{
			name: "clusterinstallref exists, no conditions set",
			existing: []runtime.Object{
				testClusterInstallRefClusterDeployment("test-fake"),
				testFakeClusterInstallWithConditions("test-fake", nil),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionUnknown,
					Reason:  hivev1.InitializedConditionReason,
					Message: "Condition Initialized",
				}})
			},
		},
		{
			name: "clusterinstallref exists, requirements met set to false",
			existing: []runtime.Object{
				testClusterInstallRefClusterDeployment("test-fake"),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallRequirementsMet,
					Status: corev1.ConditionFalse,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionUnknown,
					Reason:  hivev1.InitializedConditionReason,
					Message: "Condition Initialized",
				}})
			},
		},
		{
			name: "clusterinstallref exists, requirements met set to true",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallRequirementsMet,
					Status: corev1.ConditionTrue,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition, corev1.ConditionTrue)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionUnknown,
					Reason:  hivev1.InitializedConditionReason,
					Message: "Condition Initialized",
				}})
			},
		},
		{
			name: "clusterinstallref exists, failed true",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallFailed,
					Status: corev1.ConditionTrue,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallFailedClusterDeploymentCondition, corev1.ConditionTrue)
				testassert.AssertConditionStatus(t, cd, hivev1.ProvisionFailedCondition, corev1.ConditionTrue)
				// We don't declare provision failed in the Provisioned condition until it's terminal
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionUnknown,
					Reason:  hivev1.InitializedConditionReason,
					Message: "Condition Initialized",
				}})
			},
		},
		{
			name: "clusterinstallref exists, failed false, previously true",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake"))
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.ProvisionFailedCondition,
						corev1.ConditionTrue, "test-reason", "test-message")
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.ClusterInstallFailedClusterDeploymentCondition,
						corev1.ConditionTrue, "test-reason", "test-message")
					return cd
				}(),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallFailed,
					Status: corev1.ConditionFalse,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallFailedClusterDeploymentCondition, corev1.ConditionFalse)
				testassert.AssertConditionStatus(t, cd, hivev1.ProvisionFailedCondition, corev1.ConditionFalse)
			},
		},
		{
			name: "clusterinstallref exists, stopped, completed not set",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")))
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.RequirementsMetCondition,
						corev1.ConditionUnknown, clusterImageSetFoundReason, "test-message")
					return cd
				}(),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallStopped,
					Status: corev1.ConditionTrue,
				}, {
					Type:   hivev1.ClusterInstallCompleted,
					Status: corev1.ConditionFalse,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallStoppedClusterDeploymentCondition, corev1.ConditionTrue)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
						Reason: "InstallAttemptsLimitReached",
					},
					{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionFalse,
						Reason:  hivev1.ProvisionedReasonProvisionStopped,
						Message: "Provisioning failed terminally (see the ProvisionStopped condition for details)",
					},
				})
			},
		},
		{
			name: "clusterinstallref exists, stopped, completed false",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallStopped,
					Status: corev1.ConditionTrue,
				}, {
					Type:   hivev1.ClusterInstallCompleted,
					Status: corev1.ConditionFalse,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallStoppedClusterDeploymentCondition, corev1.ConditionTrue)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
						Reason: "InstallAttemptsLimitReached",
					},
					{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionFalse,
						Reason:  hivev1.ProvisionedReasonProvisionStopped,
						Message: "Provisioning failed terminally (see the ProvisionStopped condition for details)",
					},
				})
			},
		},
		{
			name: "clusterinstallref exists, previously stopped, now progressing",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake"))
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.ProvisionStoppedCondition,
						corev1.ConditionTrue, installAttemptsLimitReachedReason, "test-message")
					cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd, hivev1.ClusterInstallStoppedClusterDeploymentCondition,
						corev1.ConditionTrue, "test-reason", "test-message")
					return cd
				}(),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallStopped,
					Status: corev1.ConditionFalse,
				}, {
					Type:   hivev1.ClusterInstallCompleted,
					Status: corev1.ConditionFalse,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallStoppedClusterDeploymentCondition, corev1.ConditionFalse)
				testassert.AssertConditionStatus(t, cd, hivev1.ProvisionStoppedCondition, corev1.ConditionFalse)
			},
		},
		{
			name: "clusterinstallref exists, cluster metadata available partially",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterInstallRefClusterDeployment("test-fake")
					cd.Spec.ClusterMetadata = nil
					return cd
				}(),
				testFakeClusterInstallWithClusterMetadata("test-fake", hivev1.ClusterMetadata{InfraID: testInfraID}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				assert.Nil(t, cd.Spec.ClusterMetadata)
			},
		},
		{
			name: "clusterinstallref exists, cluster metadata available partially (no AdminPasswordSecretRef)",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterInstallRefClusterDeployment("test-fake")
					cd.Spec.ClusterMetadata = nil
					return cd
				}(),
				testFakeClusterInstallWithClusterMetadata("test-fake", hivev1.ClusterMetadata{
					InfraID:   testInfraID,
					ClusterID: testClusterID,
					AdminKubeconfigSecretRef: corev1.LocalObjectReference{
						Name: adminKubeconfigSecret,
					},
				}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				// Metadata wasn't copied because metadata was incomplete (no AdminPasswordSecretRef)
				assert.Nil(t, cd.Spec.ClusterMetadata)
			},
		},
		{
			name: "clusterinstallref exists, cluster metadata available",
			existing: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake"))
					cd.Spec.ClusterMetadata = nil
					return cd
				}(),
				testFakeClusterInstallWithClusterMetadata("test-fake", hivev1.ClusterMetadata{
					InfraID:   testInfraID,
					ClusterID: testClusterID,
					AdminKubeconfigSecretRef: corev1.LocalObjectReference{
						Name: adminKubeconfigSecret,
					},
					AdminPasswordSecretRef: &corev1.LocalObjectReference{
						Name: adminPasswordSecret,
					},
				}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				require.NotNil(t, cd.Spec.ClusterMetadata)
				assert.Equal(t, testInfraID, cd.Spec.ClusterMetadata.InfraID)
				assert.Equal(t, testClusterID, cd.Spec.ClusterMetadata.ClusterID)
				assert.Equal(t, adminKubeconfigSecret, cd.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name)
				assert.Equal(t, adminPasswordSecret, cd.Spec.ClusterMetadata.AdminPasswordSecretRef.Name)
			},
		},
		{
			name: "clusterinstallref exists, completed",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallCompleted,
					Status: corev1.ConditionTrue,
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallCompletedClusterDeploymentCondition, corev1.ConditionTrue)
				assert.Equal(t, true, cd.Spec.Installed)
				assert.NotNil(t, cd.Status.InstalledTimestamp)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{{
					Type:    hivev1.ProvisionedCondition,
					Status:  corev1.ConditionTrue,
					Reason:  hivev1.ProvisionedReasonProvisioned,
					Message: "Cluster is provisioned",
				}})
			},
		},
		{
			name: "clusterinstallref exists, stopped and completed",
			existing: []runtime.Object{
				testClusterDeploymentWithInitializedConditions(testClusterInstallRefClusterDeployment("test-fake")),
				testFakeClusterInstallWithConditions("test-fake", []hivev1.ClusterInstallCondition{{
					Type:   hivev1.ClusterInstallCompleted,
					Status: corev1.ConditionTrue,
				}, {
					Type:   hivev1.ClusterInstallStopped,
					Status: corev1.ConditionTrue,
					Reason: "InstallComplete",
				}}),
				testClusterImageSet(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(c client.Client, t *testing.T) {
				cd := getCD(c)
				require.NotNil(t, cd, "could not get ClusterDeployment")
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallCompletedClusterDeploymentCondition, corev1.ConditionTrue)
				testassert.AssertConditionStatus(t, cd, hivev1.ClusterInstallStoppedClusterDeploymentCondition, corev1.ConditionTrue)
				testassert.AssertConditions(t, cd, []hivev1.ClusterDeploymentCondition{
					{
						Type:   hivev1.ProvisionStoppedCondition,
						Status: corev1.ConditionTrue,
						Reason: "InstallComplete",
					},
					{
						Type:    hivev1.ProvisionedCondition,
						Status:  corev1.ConditionTrue,
						Reason:  hivev1.ProvisionedReasonProvisioned,
						Message: "Cluster is provisioned",
					},
				})
				assert.Equal(t, true, cd.Spec.Installed)
			},
		},
		{
			name: "RetryReasons: matching entry: retry",
			existing: []runtime.Object{
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testProvision(tcp.WithFailureReason("aReason")),
				testInstallConfigSecret(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			retryReasons:          &[]string{"foo", "aReason", "bar"},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				if cd := getCD(c); assert.NotNil(t, cd, "no clusterdeployment found") {
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionStoppedCondition); assert.NotNil(t, cond, "no ProvisionStopped condition") {
						assert.Equal(t, corev1.ConditionFalse, cond.Status, "expected ProvisionStopped to be False")
					}
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionedCondition); assert.NotNil(t, cond, "no Provisioned condition") {
						assert.Equal(t, hivev1.ProvisionedReasonProvisioning, cond.Reason, "expected Provisioned to be Provisioning")
					}
				}
				assert.Len(t, getProvisions(c), 2, "expected 2 ClusterProvisions to exist")
			},
		},
		{
			name: "RetryReasons: matching entry but limit reached: no retry",
			existing: []runtime.Object{
				func() runtime.Object {
					cd := testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment()))
					cd.Status.InstallRestarts = 2
					cd.Spec.InstallAttemptsLimit = pointer.Int32Ptr(2)
					return cd
				}(),
				testProvision(tcp.WithFailureReason("aReason")),
				testInstallConfigSecret(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			retryReasons: &[]string{"foo", "aReason", "bar"},
			validate: func(c client.Client, t *testing.T) {
				if cd := getCD(c); assert.NotNil(t, cd, "no clusterdeployment found") {
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionStoppedCondition); assert.NotNil(t, cond, "no ProvisionStopped condition") {
						assert.Equal(t, corev1.ConditionTrue, cond.Status, "expected ProvisionStopped to be True")
						assert.Equal(t, "InstallAttemptsLimitReached", cond.Reason, "expected ProvisionStopped Reason to be InstallAttemptsLimitReached")
					}
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionedCondition); assert.NotNil(t, cond, "no Provisioned condition") {
						assert.Equal(t, hivev1.ProvisionedReasonProvisionStopped, cond.Reason, "expected Provisioned to be ProvisionStopped")
					}
				}
				assert.Len(t, getProvisions(c), 1, "expected 1 ClusterProvision to exist")
			},
		},
		{
			name: "RetryReasons: matching entry is in most recent provision: retry",
			existing: []runtime.Object{
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testProvision(tcp.WithFailureReason("aReason"), tcp.Attempt(0), tcp.WithCreationTimestamp(time.Now().Add(-2*time.Hour))),
				testProvision(tcp.WithFailureReason("bReason"), tcp.Attempt(1), tcp.WithCreationTimestamp(time.Now().Add(-1*time.Hour))),
				testProvision(tcp.WithFailureReason("cReason"), tcp.Attempt(2), tcp.WithCreationTimestamp(time.Now())),
				testInstallConfigSecret(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			retryReasons:          &[]string{"foo", "cReason", "bar"},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				if cd := getCD(c); assert.NotNil(t, cd, "no clusterdeployment found") {
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionStoppedCondition); assert.NotNil(t, cond, "no ProvisionStopped condition") {
						assert.Equal(t, corev1.ConditionFalse, cond.Status, "expected ProvisionStopped to be False")
					}
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionedCondition); assert.NotNil(t, cond, "no Provisioned condition") {
						assert.Equal(t, hivev1.ProvisionedReasonProvisioning, cond.Reason, "expected Provisioned to be Provisioning")
					}
				}
				assert.Len(t, getProvisions(c), 4, "expected 4 ClusterProvisions to exist")
			},
		},
		{
			name: "RetryReasons: matching entry is in older provision: no retry",
			existing: []runtime.Object{
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testProvision(tcp.WithFailureReason("aReason"), tcp.Attempt(0), tcp.WithCreationTimestamp(time.Now().Add(-2*time.Hour))),
				testProvision(tcp.WithFailureReason("bReason"), tcp.Attempt(1), tcp.WithCreationTimestamp(time.Now().Add(-1*time.Hour))),
				testProvision(tcp.WithFailureReason("cReason"), tcp.Attempt(2), tcp.WithCreationTimestamp(time.Now())),
				testInstallConfigSecret(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			retryReasons: &[]string{"foo", "bReason", "bar"},
			validate: func(c client.Client, t *testing.T) {
				if cd := getCD(c); assert.NotNil(t, cd, "no clusterdeployment found") {
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionStoppedCondition); assert.NotNil(t, cond, "no ProvisionStopped condition") {
						assert.Equal(t, corev1.ConditionTrue, cond.Status, "expected ProvisionStopped to be True")
						assert.Equal(t, "FailureReasonNotRetryable", cond.Reason, "expected ProvisionStopped Reason to be FailureReasonNotRetryable")
					}
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionedCondition); assert.NotNil(t, cond, "no Provisioned condition") {
						assert.Equal(t, hivev1.ProvisionedReasonProvisionStopped, cond.Reason, "expected Provisioned to be ProvisionStopped")
					}
				}
				assert.Len(t, getProvisions(c), 3, "expected 3 ClusterProvisions to exist")
			},
		},
		{
			name: "RetryReasons: no matching entry: no retry",
			existing: []runtime.Object{
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testProvision(tcp.WithFailureReason("aReason")),
				testInstallConfigSecret(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			retryReasons: &[]string{"foo", "bReason", "bar"},
			validate: func(c client.Client, t *testing.T) {
				if cd := getCD(c); assert.NotNil(t, cd, "no clusterdeployment found") {
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionStoppedCondition); assert.NotNil(t, cond, "no ProvisionStopped condition") {
						assert.Equal(t, corev1.ConditionTrue, cond.Status, "expected ProvisionStopped to be True")
						assert.Equal(t, "FailureReasonNotRetryable", cond.Reason, "expected ProvisionStopped Reason to be FailureReasonNotRetryable")
					}
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionedCondition); assert.NotNil(t, cond, "no Provisioned condition") {
						assert.Equal(t, hivev1.ProvisionedReasonProvisionStopped, cond.Reason, "expected Provisioned to be ProvisionStopped")
					}
				}
				assert.Len(t, getProvisions(c), 1, "expected 1 ClusterProvision to exist")
			},
		},
		{
			name: "RetryReasons: no provision yet: list ignored, provision created",
			existing: []runtime.Object{
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testInstallConfigSecret(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			retryReasons:          &[]string{"foo", "bar", "baz"},
			expectPendingCreation: true,
			validate: func(c client.Client, t *testing.T) {
				if cd := getCD(c); assert.NotNil(t, cd, "no clusterdeployment found") {
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionStoppedCondition); assert.NotNil(t, cond, "no ProvisionStopped condition") {
						assert.Equal(t, corev1.ConditionFalse, cond.Status, "expected ProvisionStopped to be False")
					}
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionedCondition); assert.NotNil(t, cond, "no Provisioned condition") {
						assert.Equal(t, hivev1.ProvisionedReasonProvisioning, cond.Reason, "expected Provisioned to be Provisioning")
					}
				}
				assert.Len(t, getProvisions(c), 1, "expected 1 ClusterProvisions to exist")
			},
		},
		{
			name: "RetryReasons: empty list: no retry",
			existing: []runtime.Object{
				testClusterDeploymentWithDefaultConditions(testClusterDeploymentWithInitializedConditions(testClusterDeployment())),
				testProvision(tcp.WithFailureReason("aReason")),
				testInstallConfigSecret(),
				testSecret(corev1.SecretTypeDockerConfigJson, pullSecretSecret, corev1.DockerConfigJsonKey, "{}"),
				testSecret(corev1.SecretTypeDockerConfigJson, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			// NB: This is *not* the same as the list being absent.
			retryReasons: &[]string{},
			validate: func(c client.Client, t *testing.T) {
				if cd := getCD(c); assert.NotNil(t, cd, "no clusterdeployment found") {
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionStoppedCondition); assert.NotNil(t, cond, "no ProvisionStopped condition") {
						assert.Equal(t, corev1.ConditionTrue, cond.Status, "expected ProvisionStopped to be True")
						assert.Equal(t, "FailureReasonNotRetryable", cond.Reason, "expected ProvisionStopped Reason to be FailureReasonNotRetryable")
					}
					if cond := controllerutils.FindCondition(cd.Status.Conditions, hivev1.ProvisionedCondition); assert.NotNil(t, cond, "no Provisioned condition") {
						assert.Equal(t, hivev1.ProvisionedReasonProvisionStopped, cond.Reason, "expected Provisioned to be ProvisionStopped")
					}
				}
				assert.Len(t, getProvisions(c), 1, "expected 1 ClusterProvision to exist")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := log.WithField("controller", "clusterDeployment")
			if test.retryReasons == nil {
				readFile = fakeReadFile("")
			} else {
				b, _ := json.Marshal(*test.retryReasons)
				readFile = fakeReadFile(fmt.Sprintf(`
				{
					"retryReasons": %s
				}
				`, string(b)))
			}
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(test.existing...).Build()
			controllerExpectations := controllerutils.NewExpectations(logger)
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockRemoteClientBuilder := remoteclientmock.NewMockBuilder(mockCtrl)

			if test.platformCredentialsValidation == nil {
				test.platformCredentialsValidation = func(client.Client, *hivev1.ClusterDeployment, log.FieldLogger) (bool, error) {
					return true, nil
				}
			}
			rcd := &ReconcileClusterDeployment{
				Client:                                  fakeClient,
				scheme:                                  scheme.Scheme,
				logger:                                  logger,
				expectations:                            controllerExpectations,
				remoteClusterAPIClientBuilder:           func(*hivev1.ClusterDeployment) remoteclient.Builder { return mockRemoteClientBuilder },
				validateCredentialsForClusterDeployment: test.platformCredentialsValidation,
				watchingClusterInstall: map[string]struct{}{
					(schema.GroupVersionKind{Group: "hive.openshift.io", Version: "v1", Kind: "FakeClusterInstall"}).String(): {},
				},
				releaseImageVerifier: test.riVerifier,
			}

			if test.reconcilerSetup != nil {
				test.reconcilerSetup(rcd)
			}

			reconcileRequest := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testName,
					Namespace: testNamespace,
				},
			}

			if test.pendingCreation {
				controllerExpectations.ExpectCreations(reconcileRequest.String(), 1)
			}

			if test.expectConsoleRouteFetch {
				mockRemoteClientBuilder.EXPECT().Build().Return(testRemoteClusterAPIClient(), nil)
			}

			result, err := rcd.Reconcile(context.TODO(), reconcileRequest)

			if test.validate != nil {
				test.validate(fakeClient, t)
			}

			if err != nil && !test.expectErr {
				t.Errorf("Unexpected error: %v", err)
			}
			if err == nil && test.expectErr {
				t.Errorf("Expected error but got none")
			}

			if test.expectedRequeueAfter == 0 {
				assert.Zero(t, result.RequeueAfter, "expected empty requeue after")
			} else {
				assert.InDelta(t, test.expectedRequeueAfter, result.RequeueAfter, float64(10*time.Second), "unexpected requeue after")
			}
			assert.Equal(t, test.expectExplicitRequeue, result.Requeue, "unexpected requeue")

			actualPendingCreation := !controllerExpectations.SatisfiedExpectations(reconcileRequest.String())
			assert.Equal(t, test.expectPendingCreation, actualPendingCreation, "unexpected pending creation")
		})
	}
}

func TestClusterDeploymentReconcileResults(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)

	tests := []struct {
		name                     string
		existing                 []runtime.Object
		exptectedReconcileResult reconcile.Result
	}{
		{
			name: "Requeue after adding finalizer",
			existing: []runtime.Object{
				testClusterDeploymentWithoutFinalizer(),
			},
			exptectedReconcileResult: reconcile.Result{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := log.WithField("controller", "clusterDeployment")
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(test.existing...).Build()
			controllerExpectations := controllerutils.NewExpectations(logger)
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			mockRemoteClientBuilder := remoteclientmock.NewMockBuilder(mockCtrl)
			rcd := &ReconcileClusterDeployment{
				Client:                        fakeClient,
				scheme:                        scheme.Scheme,
				logger:                        logger,
				expectations:                  controllerExpectations,
				remoteClusterAPIClientBuilder: func(*hivev1.ClusterDeployment) remoteclient.Builder { return mockRemoteClientBuilder },
			}

			reconcileResult, err := rcd.Reconcile(context.TODO(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testName,
					Namespace: testNamespace,
				},
			})

			assert.NoError(t, err, "unexpected error")

			assert.Equal(t, test.exptectedReconcileResult, reconcileResult, "unexpected reconcile result")
		})
	}
}

func TestCalculateNextProvisionTime(t *testing.T) {
	cases := []struct {
		name             string
		failureTime      time.Time
		attempt          int
		expectedNextTime time.Time
	}{
		{
			name:             "first attempt",
			failureTime:      time.Date(2019, time.July, 16, 0, 0, 0, 0, time.UTC),
			attempt:          0,
			expectedNextTime: time.Date(2019, time.July, 16, 0, 1, 0, 0, time.UTC),
		},
		{
			name:             "second attempt",
			failureTime:      time.Date(2019, time.July, 16, 0, 0, 0, 0, time.UTC),
			attempt:          1,
			expectedNextTime: time.Date(2019, time.July, 16, 0, 2, 0, 0, time.UTC),
		},
		{
			name:             "third attempt",
			failureTime:      time.Date(2019, time.July, 16, 0, 0, 0, 0, time.UTC),
			attempt:          2,
			expectedNextTime: time.Date(2019, time.July, 16, 0, 4, 0, 0, time.UTC),
		},
		{
			name:             "eleventh attempt",
			failureTime:      time.Date(2019, time.July, 16, 0, 0, 0, 0, time.UTC),
			attempt:          10,
			expectedNextTime: time.Date(2019, time.July, 16, 17, 4, 0, 0, time.UTC),
		},
		{
			name:             "twelfth attempt",
			failureTime:      time.Date(2019, time.July, 16, 0, 0, 0, 0, time.UTC),
			attempt:          11,
			expectedNextTime: time.Date(2019, time.July, 17, 0, 0, 0, 0, time.UTC),
		},
		{
			name:             "thirteenth attempt",
			failureTime:      time.Date(2019, time.July, 16, 0, 0, 0, 0, time.UTC),
			attempt:          12,
			expectedNextTime: time.Date(2019, time.July, 17, 0, 0, 0, 0, time.UTC),
		},
		{
			name:             "millionth attempt",
			failureTime:      time.Date(2019, time.July, 16, 0, 0, 0, 0, time.UTC),
			attempt:          999999,
			expectedNextTime: time.Date(2019, time.July, 17, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actualNextTime := calculateNextProvisionTime(tc.failureTime, tc.attempt, log.WithField("controller", "clusterDeployment"))
			assert.Equal(t, tc.expectedNextTime.String(), actualNextTime.String(), "unexpected next provision time")
		})
	}
}

func TestDeleteStaleProvisions(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)
	cases := []struct {
		name             string
		existingAttempts []int
		expectedAttempts []int
	}{
		{
			name: "none",
		},
		{
			name:             "one",
			existingAttempts: []int{0},
			expectedAttempts: []int{0},
		},
		{
			name:             "three",
			existingAttempts: []int{0, 1, 2},
			expectedAttempts: []int{0, 1, 2},
		},
		{
			name:             "four",
			existingAttempts: []int{0, 1, 2, 3},
			expectedAttempts: []int{0, 2, 3},
		},
		{
			name:             "five",
			existingAttempts: []int{0, 1, 2, 3, 4},
			expectedAttempts: []int{0, 3, 4},
		},
		{
			name:             "five mixed order",
			existingAttempts: []int{10, 3, 7, 8, 1},
			expectedAttempts: []int{1, 8, 10},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provisions := make([]runtime.Object, len(tc.existingAttempts))
			for i, a := range tc.existingAttempts {
				provisions[i] = testProvision(tcp.Failed(), tcp.Attempt(a))
			}
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(provisions...).Build()
			rcd := &ReconcileClusterDeployment{
				Client: fakeClient,
				scheme: scheme.Scheme,
			}
			rcd.deleteStaleProvisions(getProvisions(fakeClient), log.WithField("test", "TestDeleteStaleProvisions"))
			actualAttempts := []int{}
			for _, p := range getProvisions(fakeClient) {
				actualAttempts = append(actualAttempts, p.Spec.Attempt)
			}
			assert.ElementsMatch(t, tc.expectedAttempts, actualAttempts, "unexpected provisions kept")
		})
	}
}

func TestDeleteOldFailedProvisions(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)
	cases := []struct {
		name                                    string
		totalProvisions                         int
		failedProvisionsMoreThanSevenDaysOld    int
		expectedNumberOfProvisionsAfterDeletion int
	}{
		{
			name:                                    "One failed provision more than 7 days old",
			totalProvisions:                         2,
			failedProvisionsMoreThanSevenDaysOld:    1,
			expectedNumberOfProvisionsAfterDeletion: 1,
		},
		{
			name:                                    "No failed provision more than 7 days old",
			totalProvisions:                         2,
			failedProvisionsMoreThanSevenDaysOld:    0,
			expectedNumberOfProvisionsAfterDeletion: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provisions := make([]runtime.Object, tc.totalProvisions)
			for i := 0; i < tc.totalProvisions; i++ {
				if i < tc.failedProvisionsMoreThanSevenDaysOld {
					provisions[i] = testProvision(
						tcp.Failed(),
						tcp.WithCreationTimestamp(time.Now().Add(-7*24*time.Hour)),
						tcp.Attempt(i))
				} else {
					provisions[i] = testProvision(
						tcp.Failed(),
						tcp.WithCreationTimestamp(time.Now()),
						tcp.Attempt(i))
				}
			}
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(provisions...).Build()
			rcd := &ReconcileClusterDeployment{
				Client: fakeClient,
				scheme: scheme.Scheme,
			}
			rcd.deleteOldFailedProvisions(getProvisions(fakeClient), log.WithField("test", "TestDeleteOldFailedProvisions"))
			assert.Len(t, getProvisions(fakeClient), tc.expectedNumberOfProvisionsAfterDeletion, "unexpected provisions kept")
		})
	}
}

func testEmptyClusterDeployment() *hivev1.ClusterDeployment {
	cd := &hivev1.ClusterDeployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: hivev1.SchemeGroupVersion.String(),
			Kind:       "ClusterDeployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       testName,
			Namespace:  testNamespace,
			Finalizers: []string{hivev1.FinalizerDeprovision},
			UID:        types.UID("1234"),
		},
	}
	return cd
}

func testInstallConfigSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      installConfigSecretName,
		},
		Data: map[string][]byte{
			"install-config.yaml": []byte(testAWSIC),
		},
	}
}

func testClusterDeployment() *hivev1.ClusterDeployment {
	cd := testEmptyClusterDeployment()

	cd.Spec = hivev1.ClusterDeploymentSpec{
		ClusterName: testClusterName,
		PullSecretRef: &corev1.LocalObjectReference{
			Name: pullSecretSecret,
		},
		Platform: hivev1.Platform{
			AWS: &hivev1aws.Platform{
				CredentialsSecretRef: corev1.LocalObjectReference{
					Name: "aws-credentials",
				},
				Region: "us-east-1",
			},
		},
		Provisioning: &hivev1.Provisioning{
			InstallConfigSecretRef: &corev1.LocalObjectReference{Name: installConfigSecretName},
		},
		ClusterMetadata: &hivev1.ClusterMetadata{
			ClusterID:                testClusterID,
			InfraID:                  testInfraID,
			AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: adminKubeconfigSecret},
			AdminPasswordSecretRef:   &corev1.LocalObjectReference{Name: adminPasswordSecret},
		},
	}

	if cd.Labels == nil {
		cd.Labels = make(map[string]string, 2)
	}
	cd.Labels[hivev1.HiveClusterPlatformLabel] = "aws"
	cd.Labels[hivev1.HiveClusterRegionLabel] = "us-east-1"

	cd.Status = hivev1.ClusterDeploymentStatus{
		InstallerImage: pointer.StringPtr("installer-image:latest"),
		CLIImage:       pointer.StringPtr("cli:latest"),
	}

	return cd
}

func testClusterDeploymentWithDefaultConditions(cd *hivev1.ClusterDeployment) *hivev1.ClusterDeployment {
	cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd,
		hivev1.InstallImagesNotResolvedCondition,
		corev1.ConditionFalse,
		imagesResolvedReason,
		imagesResolvedMsg)
	cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd,
		hivev1.AuthenticationFailureClusterDeploymentCondition,
		corev1.ConditionFalse,
		platformAuthSuccessReason,
		"Platform credentials passed authentication check")
	cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd,
		hivev1.ProvisionStoppedCondition,
		corev1.ConditionFalse,
		"ProvisionNotStopped",
		"Provision is not stopped")
	cd.Status.Conditions = addOrUpdateClusterDeploymentCondition(*cd,
		hivev1.RequirementsMetCondition,
		corev1.ConditionTrue,
		"AllRequirementsMet",
		"All pre-provision requirements met")
	return cd
}

func testClusterDeploymentWithInitializedConditions(cd *hivev1.ClusterDeployment) *hivev1.ClusterDeployment {
	for _, condition := range clusterDeploymentConditions {
		cd.Status.Conditions = append(cd.Status.Conditions, hivev1.ClusterDeploymentCondition{
			Status:  corev1.ConditionUnknown,
			Type:    condition,
			Reason:  hivev1.InitializedConditionReason,
			Message: "Condition Initialized",
		})
	}
	return cd
}

func testClusterDeploymentCustomization(name string) *hivev1.ClusterDeploymentCustomization {
	cdc := &hivev1.ClusterDeploymentCustomization{}
	cdc.Name = name
	cdc.Namespace = testNamespace
	return cdc
}

func testInstalledClusterDeployment(installedAt time.Time) *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	cd.Spec.Installed = true
	cd.Status.InstalledTimestamp = &metav1.Time{Time: installedAt}
	cd.Status.APIURL = "http://quite.fake.com"
	cd.Status.WebConsoleURL = "http://quite.fake.com/console"
	return cd
}

func testClusterInstallRefClusterDeployment(name string) *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	cd.Spec.Provisioning = nil
	cd.Spec.ClusterInstallRef = &hivev1.ClusterInstallLocalReference{
		Group:   "hive.openshift.io",
		Version: "v1",
		Kind:    "FakeClusterInstall",
		Name:    name,
	}
	return cd
}

func testClusterDeploymentWithoutFinalizer() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	cd.Finalizers = []string{}
	return cd
}

func testClusterDeploymentWithoutPlatformLabel() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	delete(cd.Labels, hivev1.HiveClusterPlatformLabel)
	return cd
}

func testClusterDeploymentWithoutRegionLabel() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	delete(cd.Labels, hivev1.HiveClusterRegionLabel)
	return cd
}

func testDeletedClusterDeployment() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	now := metav1.Now()
	cd.DeletionTimestamp = &now
	return cd
}

func testDeletedClusterDeploymentWithoutFinalizer() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	now := metav1.Now()
	cd.DeletionTimestamp = &now
	cd.Finalizers = []string{}
	return cd
}

func testExpiredClusterDeployment() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	cd.CreationTimestamp = metav1.Time{Time: metav1.Now().Add(-60 * time.Minute)}
	if cd.Annotations == nil {
		cd.Annotations = make(map[string]string, 1)
	}
	cd.Annotations[deleteAfterAnnotation] = "5m"
	return cd
}

func testClusterDeploymentWithProvision() *hivev1.ClusterDeployment {
	cd := testClusterDeployment()
	cd.Status.ProvisionRef = &corev1.LocalObjectReference{Name: provisionName}
	return cd
}

func testEmptyFakeClusterInstall(name string) *unstructured.Unstructured {
	fake := &unstructured.Unstructured{}
	fake.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "hive.openshift.io",
		Version: "v1",
		Kind:    "FakeClusterInstall",
	})
	fake.SetNamespace(testNamespace)
	fake.SetName(name)
	unstructured.SetNestedField(fake.UnstructuredContent(), map[string]interface{}{}, "spec")
	unstructured.SetNestedField(fake.UnstructuredContent(), map[string]interface{}{}, "status")
	return fake
}

func testFakeClusterInstall(name string) *unstructured.Unstructured {
	fake := testEmptyFakeClusterInstall(name)
	unstructured.SetNestedField(fake.UnstructuredContent(), map[string]interface{}{
		"name": testClusterImageSetName,
	}, "spec", "imageSetRef")
	return fake
}

func testFakeClusterInstallWithConditions(name string, conditions []hivev1.ClusterInstallCondition) *unstructured.Unstructured {
	fake := testFakeClusterInstall(name)

	value := []interface{}{}
	for _, c := range conditions {
		value = append(value, map[string]interface{}{
			"type":    string(c.Type),
			"status":  string(c.Status),
			"reason":  c.Reason,
			"message": c.Message,
		})
	}

	unstructured.SetNestedField(fake.UnstructuredContent(), value, "status", "conditions")
	return fake
}

func testFakeClusterInstallWithClusterMetadata(name string, metadata hivev1.ClusterMetadata) *unstructured.Unstructured {
	fake := testFakeClusterInstall(name)

	value := map[string]interface{}{
		"clusterID": metadata.ClusterID,
		"infraID":   metadata.InfraID,
		"adminKubeconfigSecretRef": map[string]interface{}{
			"name": metadata.AdminKubeconfigSecretRef.Name,
		},
	}

	if metadata.AdminPasswordSecretRef != nil {
		value["adminPasswordSecretRef"] = map[string]interface{}{
			"name": metadata.AdminPasswordSecretRef.Name,
		}
	}

	unstructured.SetNestedField(fake.UnstructuredContent(), value, "spec", "clusterMetadata")
	return fake
}

func testProvision(opts ...tcp.Option) *hivev1.ClusterProvision {
	cd := testClusterDeployment()
	provision := tcp.FullBuilder(testNamespace, provisionName).Build(tcp.WithClusterDeploymentRef(testName))

	controllerutil.SetControllerReference(cd, provision, scheme.Scheme)

	for _, opt := range opts {
		opt(provision)
	}

	return provision
}

func testSuccessfulProvision() *hivev1.ClusterProvision {
	return testProvision(tcp.Successful(
		testClusterID, testInfraID, adminKubeconfigSecret, adminPasswordSecret))
}

func testMetadataConfigMap() *corev1.ConfigMap {
	cm := &corev1.ConfigMap{}
	cm.Name = metadataName
	cm.Namespace = testNamespace
	metadataJSON := `{
		"aws": {
			"identifier": [{"openshiftClusterID": "testFooClusterUUID"}]
		}
	}`
	cm.Data = map[string]string{"metadata.json": metadataJSON}
	return cm
}

func testSecret(secretType corev1.SecretType, name, key, value string) *corev1.Secret {
	return testSecretWithNamespace(secretType, name, testNamespace, key, value)
}

func testSecretWithNamespace(secretType corev1.SecretType, name, namespace, key, value string) *corev1.Secret {
	s := &corev1.Secret{
		Type: secretType,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			key: []byte(value),
		},
	}
	return s
}

func testRemoteClusterAPIClient() client.Client {
	remoteClusterRouteObject := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      remoteClusterRouteObjectName,
			Namespace: remoteClusterRouteObjectNamespace,
		},
	}
	remoteClusterRouteObject.Spec.Host = "bar-api.clusters.example.com:6443/console"

	return fake.NewClientBuilder().WithRuntimeObjects(remoteClusterRouteObject).Build()
}

func testClusterImageSet() *hivev1.ClusterImageSet {
	cis := &hivev1.ClusterImageSet{}
	cis.Name = testClusterImageSetName
	cis.Spec.ReleaseImage = "test-release-image:latest"
	return cis
}

func testDNSZone() *hivev1.DNSZone {
	zone := &hivev1.DNSZone{}
	zone.Name = testName + "-zone"
	zone.Namespace = testNamespace
	zone.OwnerReferences = append(
		zone.OwnerReferences,
		*metav1.NewControllerRef(
			testClusterDeployment(),
			schema.GroupVersionKind{
				Group:   "hive.openshift.io",
				Version: "v1",
				Kind:    "clusterdeployment",
			},
		),
	)
	return zone
}

func testDNSZoneWithFinalizer() *hivev1.DNSZone {
	zone := testDNSZone()
	zone.ObjectMeta.Finalizers = []string{"hive.openshift.io/dnszone"}
	return zone
}

func testAvailableDNSZone() *hivev1.DNSZone {
	zone := testDNSZoneWithFinalizer()
	zone.Status.Conditions = []hivev1.DNSZoneCondition{
		{
			Type:    hivev1.ZoneAvailableDNSZoneCondition,
			Status:  corev1.ConditionTrue,
			Reason:  dnsReadyReason,
			Message: "DNS Zone available",
			LastTransitionTime: metav1.Time{
				Time: time.Now(),
			},
		},
	}
	return zone
}

func testDNSZoneWithInvalidCredentialsCondition() *hivev1.DNSZone {
	zone := testDNSZone()
	zone.Status.Conditions = []hivev1.DNSZoneCondition{
		{
			Type:   hivev1.InsufficientCredentialsCondition,
			Status: corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{
				Time: time.Now(),
			},
		},
	}
	return zone
}
func testDNSZoneWithAPIOptInRequiredCondition() *hivev1.DNSZone {
	zone := testDNSZone()
	zone.Status.Conditions = []hivev1.DNSZoneCondition{
		{
			Type:   hivev1.APIOptInRequiredCondition,
			Status: corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{
				Time: time.Now(),
			},
		},
	}
	return zone
}

func testDNSZoneWithAuthenticationFailureCondition() *hivev1.DNSZone {
	zone := testDNSZone()
	zone.Status.Conditions = []hivev1.DNSZoneCondition{
		{
			Type:   hivev1.AuthenticationFailureCondition,
			Status: corev1.ConditionTrue,
			LastTransitionTime: metav1.Time{
				Time: time.Now(),
			},
		},
	}
	return zone
}

func testDNSZoneWithDNSErrorCondition() *hivev1.DNSZone {
	zone := testDNSZone()
	zone.Status.Conditions = []hivev1.DNSZoneCondition{
		{
			Type:    hivev1.GenericDNSErrorsCondition,
			Status:  corev1.ConditionTrue,
			Reason:  "CloudError",
			Message: "Some cloud error occurred",
			LastTransitionTime: metav1.Time{
				Time: time.Now(),
			},
		},
	}
	return zone
}

func addOrUpdateClusterDeploymentCondition(cd hivev1.ClusterDeployment,
	condition hivev1.ClusterDeploymentConditionType, status corev1.ConditionStatus,
	reason string, message string) []hivev1.ClusterDeploymentCondition {
	newConditions := cd.Status.Conditions
	changed := false
	for i, cond := range newConditions {
		if cond.Type == condition {
			cond.Status = status
			cond.Reason = reason
			cond.Message = message
			newConditions[i] = cond
			changed = true
			break
		}
	}
	if !changed {
		newConditions = append(newConditions, hivev1.ClusterDeploymentCondition{
			Type:    condition,
			Status:  status,
			Reason:  reason,
			Message: message,
		})
	}
	return newConditions
}

// sanitizeConditions scrubs the condition list for each CD in `cds` so they can reasonably be compared
func sanitizeConditions(cds ...*hivev1.ClusterDeployment) {
	for _, cd := range cds {
		cd.Status.Conditions = controllerutils.SortClusterDeploymentConditions(cd.Status.Conditions)
		for i := range cd.Status.Conditions {
			cd.Status.Conditions[i].LastProbeTime = metav1.Time{}
			cd.Status.Conditions[i].LastTransitionTime = metav1.Time{}
		}
	}
}

func getJob(c client.Client, name string) *batchv1.Job {
	job := &batchv1.Job{}
	err := c.Get(context.TODO(), client.ObjectKey{Name: name, Namespace: testNamespace}, job)
	if err == nil {
		return job
	}
	return nil
}

func TestUpdatePullSecretInfo(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)
	testPullSecret1 := `{"auths": {"registry.svc.ci.okd.org": {"auth": "dXNljlfjldsfSDD"}}}`

	tests := []struct {
		name       string
		existingCD []runtime.Object
		validate   func(*testing.T, *corev1.Secret)
	}{
		{
			name: "update existing merged pull secret with the new pull secret",
			existingCD: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ClusterMetadata.AdminKubeconfigSecretRef = corev1.LocalObjectReference{Name: adminKubeconfigSecret}
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockercfg, pullSecretSecret, corev1.DockerConfigJsonKey, testPullSecret1),
				testSecret(corev1.SecretTypeDockercfg, constants.GetMergedPullSecretName(testClusterDeployment()), corev1.DockerConfigJsonKey, "{}"),
			},
			validate: func(t *testing.T, pullSecretObj *corev1.Secret) {
				pullSecret, ok := pullSecretObj.Data[corev1.DockerConfigJsonKey]
				if !ok {
					t.Error("Error getting pull secret")
				}
				assert.Equal(t, string(pullSecret), testPullSecret1)
			},
		},
		{
			name: "Add a new merged pull secret",
			existingCD: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := testClusterDeploymentWithInitializedConditions(testClusterDeployment())
					cd.Spec.ClusterMetadata.AdminKubeconfigSecretRef = corev1.LocalObjectReference{Name: adminKubeconfigSecret}
					return cd
				}(),
				testSecret(corev1.SecretTypeOpaque, adminKubeconfigSecret, "kubeconfig", adminKubeconfig),
				testSecret(corev1.SecretTypeDockercfg, pullSecretSecret, corev1.DockerConfigJsonKey, testPullSecret1),
			},
			validate: func(t *testing.T, pullSecretObj *corev1.Secret) {
				assert.Equal(t, testClusterDeployment().Name, pullSecretObj.Labels[constants.ClusterDeploymentNameLabel], "incorrect cluster deployment name label")
				assert.Equal(t, constants.SecretTypeMergedPullSecret, pullSecretObj.Labels[constants.SecretTypeLabel], "incorrect secret type label")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(test.existingCD...).Build()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			mockRemoteClientBuilder := remoteclientmock.NewMockBuilder(mockCtrl)
			rcd := &ReconcileClusterDeployment{
				Client:                        fakeClient,
				scheme:                        scheme.Scheme,
				logger:                        log.WithField("controller", "clusterDeployment"),
				remoteClusterAPIClientBuilder: func(*hivev1.ClusterDeployment) remoteclient.Builder { return mockRemoteClientBuilder },
				validateCredentialsForClusterDeployment: func(client.Client, *hivev1.ClusterDeployment, log.FieldLogger) (bool, error) {
					return true, nil
				},
			}

			_, err := rcd.Reconcile(context.TODO(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      testName,
					Namespace: testNamespace,
				},
			})
			assert.NoError(t, err, "unexpected error")

			cd := getCDFromClient(rcd.Client)
			mergedSecretName := constants.GetMergedPullSecretName(cd)
			existingPullSecretObj := &corev1.Secret{}
			err = rcd.Get(context.TODO(), types.NamespacedName{Name: mergedSecretName, Namespace: cd.Namespace}, existingPullSecretObj)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if test.validate != nil {
				test.validate(t, existingPullSecretObj)
			}
		})
	}
}

func getCDWithoutPullSecret() *hivev1.ClusterDeployment {
	cd := testEmptyClusterDeployment()

	cd.Spec = hivev1.ClusterDeploymentSpec{
		ClusterName: testClusterName,
		Platform: hivev1.Platform{
			AWS: &hivev1aws.Platform{
				CredentialsSecretRef: corev1.LocalObjectReference{
					Name: "aws-credentials",
				},
				Region: "us-east-1",
			},
		},
		ClusterMetadata: &hivev1.ClusterMetadata{
			ClusterID:                testClusterID,
			InfraID:                  testInfraID,
			AdminKubeconfigSecretRef: corev1.LocalObjectReference{Name: adminKubeconfigSecret},
		},
	}
	cd.Status = hivev1.ClusterDeploymentStatus{
		InstallerImage: pointer.StringPtr("installer-image:latest"),
	}
	return cd
}

func getCDFromClient(c client.Client) *hivev1.ClusterDeployment {
	cd := &hivev1.ClusterDeployment{}
	err := c.Get(context.TODO(), client.ObjectKey{Name: testName, Namespace: testNamespace}, cd)
	if err == nil {
		return cd
	}
	return nil
}

func createGlobalPullSecretObj(secretType corev1.SecretType, name, key, value string) *corev1.Secret {
	secret := &corev1.Secret{
		Type: secretType,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: constants.DefaultHiveNamespace,
		},
		Data: map[string][]byte{
			key: []byte(value),
		},
	}
	return secret
}

func TestMergePullSecrets(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)

	tests := []struct {
		name                    string
		localPullSecret         string
		globalPullSecret        string
		mergedPullSecret        string
		existingObjs            []runtime.Object
		expectedErr             bool
		addGlobalSecretToHiveNs bool
	}{
		{
			name:             "merged pull secret should be be equal to local secret",
			localPullSecret:  `{"auths": {"registry.svc.ci.okd.org": {"auth": "dXNljlfjldsfSDD"}}}`,
			mergedPullSecret: `{"auths": {"registry.svc.ci.okd.org": {"auth": "dXNljlfjldsfSDD"}}}`,
			existingObjs: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := getCDWithoutPullSecret()
					cd.Spec.PullSecretRef = &corev1.LocalObjectReference{
						Name: pullSecretSecret,
					}
					return cd
				}(),
			},
		},
		{
			name:             "merged pull secret should be be equal to global pull secret",
			globalPullSecret: `{"auths": {"registry.svc.ci.okd.org": {"auth": "dXNljlfjldsfSDD"}}}`,
			mergedPullSecret: `{"auths": {"registry.svc.ci.okd.org": {"auth": "dXNljlfjldsfSDD"}}}`,
			existingObjs: []runtime.Object{
				getCDWithoutPullSecret(),
			},
			addGlobalSecretToHiveNs: true,
		},
		{
			name:             "Both local secret and global pull secret available",
			localPullSecret:  `{"auths": {"registry.svc.ci.okd.org": {"auth": "dXNljlfjldsfSDD"}}}`,
			globalPullSecret: `{"auths":{"cloud.okd.com":{"auth":"b34xVjWERckjfUyV1pMQTc=","email":"abc@xyz.com"}}}`,
			mergedPullSecret: `{"auths":{"cloud.okd.com":{"auth":"b34xVjWERckjfUyV1pMQTc=","email":"abc@xyz.com"},"registry.svc.ci.okd.org":{"auth":"dXNljlfjldsfSDD"}}}`,
			existingObjs: []runtime.Object{
				func() *hivev1.ClusterDeployment {
					cd := getCDWithoutPullSecret()
					cd.Spec.PullSecretRef = &corev1.LocalObjectReference{
						Name: pullSecretSecret,
					}
					return cd
				}(),
			},
			addGlobalSecretToHiveNs: true,
		},
		{
			name:             "global pull secret does not exist in Hive namespace",
			globalPullSecret: `{"auths": {"registry.svc.ci.okd.org": {"auth": "dXNljlfjldsfSDD"}}}`,
			existingObjs: []runtime.Object{
				getCDWithoutPullSecret(),
			},
			addGlobalSecretToHiveNs: false,
			expectedErr:             true,
		},
		{
			name: "Test should fail as local an global pull secret is not available",
			existingObjs: []runtime.Object{
				getCDWithoutPullSecret(),
			},
			expectedErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.globalPullSecret != "" && test.addGlobalSecretToHiveNs == true {
				globalPullSecretObj := createGlobalPullSecretObj(corev1.SecretTypeDockerConfigJson, globalPullSecret, corev1.DockerConfigJsonKey, test.globalPullSecret)
				test.existingObjs = append(test.existingObjs, globalPullSecretObj)
			}
			if test.localPullSecret != "" {
				localSecretObject := testSecret(corev1.SecretTypeDockercfg, pullSecretSecret, corev1.DockerConfigJsonKey, test.localPullSecret)
				test.existingObjs = append(test.existingObjs, localSecretObject)
			}
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(test.existingObjs...).Build()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			mockRemoteClientBuilder := remoteclientmock.NewMockBuilder(mockCtrl)
			rcd := &ReconcileClusterDeployment{
				Client:                        fakeClient,
				scheme:                        scheme.Scheme,
				logger:                        log.WithField("controller", "clusterDeployment"),
				remoteClusterAPIClientBuilder: func(*hivev1.ClusterDeployment) remoteclient.Builder { return mockRemoteClientBuilder },
			}

			cd := getCDFromClient(rcd.Client)
			if test.globalPullSecret != "" {
				os.Setenv(constants.GlobalPullSecret, globalPullSecret)
			}
			defer os.Unsetenv(constants.GlobalPullSecret)

			expetedPullSecret, err := rcd.mergePullSecrets(cd, rcd.logger)
			if test.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			if test.mergedPullSecret != "" {
				assert.Equal(t, test.mergedPullSecret, expetedPullSecret)
			}
		})
	}
}

func TestCopyInstallLogSecret(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)

	tests := []struct {
		name                    string
		existingObjs            []runtime.Object
		existingEnvVars         []corev1.EnvVar
		expectedErr             bool
		expectedNumberOfSecrets int
	}{
		{
			name: "copies secret",
			existingObjs: []runtime.Object{
				testSecretWithNamespace(corev1.SecretTypeOpaque, installLogSecret, "hive", "cloud", "cloudsecret"),
			},
			expectedNumberOfSecrets: 1,
			existingEnvVars: []corev1.EnvVar{
				{
					Name:  constants.InstallLogsCredentialsSecretRefEnvVar,
					Value: installLogSecret,
				},
			},
		},
		{
			name:        "missing secret",
			expectedErr: true,
			existingEnvVars: []corev1.EnvVar{
				{
					Name:  constants.InstallLogsCredentialsSecretRefEnvVar,
					Value: installLogSecret,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(test.existingObjs...).Build()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			mockRemoteClientBuilder := remoteclientmock.NewMockBuilder(mockCtrl)
			rcd := &ReconcileClusterDeployment{
				Client:                        fakeClient,
				scheme:                        scheme.Scheme,
				logger:                        log.WithField("controller", "clusterDeployment"),
				remoteClusterAPIClientBuilder: func(*hivev1.ClusterDeployment) remoteclient.Builder { return mockRemoteClientBuilder },
			}

			for _, envVar := range test.existingEnvVars {
				if err := os.Setenv(envVar.Name, envVar.Value); err == nil {
					defer func() {
						if err := os.Unsetenv(envVar.Name); err != nil {
							t.Error(err)
						}
					}()
				} else {
					t.Error(err)
				}
			}

			err := rcd.copyInstallLogSecret(testNamespace, test.existingEnvVars)
			secretList := &corev1.SecretList{}
			listErr := rcd.List(context.TODO(), secretList, client.InNamespace(testNamespace))

			if test.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			assert.NoError(t, listErr, "Listing secrets returned an unexpected error")
			assert.Equal(t, test.expectedNumberOfSecrets, len(secretList.Items), "Number of secrets different than expected")
		})
	}
}

func TestEnsureManagedDNSZone(t *testing.T) {
	apis.AddToScheme(scheme.Scheme)

	goodDNSZone := func() *hivev1.DNSZone {
		return testdnszone.Build(
			dnsZoneBase(),
			testdnszone.WithControllerOwnerReference(testclusterdeployment.Build(
				clusterDeploymentBase(),
			)),
			testdnszone.WithCondition(hivev1.DNSZoneCondition{
				Type: hivev1.ZoneAvailableDNSZoneCondition,
			}),
		)
	}
	tests := []struct {
		name                         string
		existingObjs                 []runtime.Object
		existingEnvVars              []corev1.EnvVar
		clusterDeployment            *hivev1.ClusterDeployment
		expectedUpdated              bool
		expectedResult               reconcile.Result
		expectedErr                  bool
		expectedDNSZone              *hivev1.DNSZone
		expectedDNSNotReadyCondition *hivev1.ClusterDeploymentCondition
	}{
		{
			name: "unsupported platform",
			clusterDeployment: testclusterdeployment.Build(
				testclusterdeployment.WithNamespace(testNamespace),
				testclusterdeployment.WithName(testName),
			),
			expectedErr: true,
			expectedDNSNotReadyCondition: &hivev1.ClusterDeploymentCondition{
				Type:   hivev1.DNSNotReadyCondition,
				Status: corev1.ConditionTrue,
				Reason: dnsUnsupportedPlatformReason,
			},
		},
		{
			name: "create zone",
			clusterDeployment: testclusterdeployment.Build(
				clusterDeploymentBase(),
			),
			expectedUpdated: true,
			expectedDNSZone: goodDNSZone(),
		},
		{
			name: "zone already exists and is owned by clusterdeployment",
			existingObjs: []runtime.Object{
				goodDNSZone(),
			},
			clusterDeployment: testclusterdeployment.Build(
				clusterDeploymentBase(),
				testclusterdeployment.WithCondition(hivev1.ClusterDeploymentCondition{
					Type:   hivev1.DNSNotReadyCondition,
					Status: corev1.ConditionUnknown,
				}),
			),
			expectedUpdated: true,
			expectedResult:  reconcile.Result{Requeue: true, RequeueAfter: defaultDNSNotReadyTimeout},
			expectedDNSNotReadyCondition: &hivev1.ClusterDeploymentCondition{
				Type:   hivev1.DNSNotReadyCondition,
				Status: corev1.ConditionTrue,
				Reason: dnsNotReadyReason,
			},
		},
		{
			name: "zone already exists but is not owned by clusterdeployment",
			existingObjs: []runtime.Object{
				testdnszone.Build(
					dnsZoneBase(),
				),
			},
			clusterDeployment: testclusterdeployment.Build(
				clusterDeploymentBase(),
			),
			expectedUpdated: true,
			expectedErr:     true,
			expectedDNSNotReadyCondition: &hivev1.ClusterDeploymentCondition{
				Type:   hivev1.DNSNotReadyCondition,
				Status: corev1.ConditionTrue,
				Reason: dnsZoneResourceConflictReason,
			},
		},
		{
			name: "zone already exists and is owned by clusterdeployment, but has timed out",
			existingObjs: []runtime.Object{
				goodDNSZone(),
			},
			clusterDeployment: testclusterdeployment.Build(
				clusterDeploymentBase(),
				testclusterdeployment.WithCondition(hivev1.ClusterDeploymentCondition{
					Type:               hivev1.DNSNotReadyCondition,
					Status:             corev1.ConditionTrue,
					Reason:             dnsNotReadyReason,
					LastProbeTime:      metav1.Time{Time: time.Now().Add(-20 * time.Minute)},
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-20 * time.Minute)},
				}),
			),
			expectedUpdated: true,
			expectedResult:  reconcile.Result{Requeue: true},
			expectedDNSNotReadyCondition: &hivev1.ClusterDeploymentCondition{
				Type:   hivev1.DNSNotReadyCondition,
				Status: corev1.ConditionTrue,
				Reason: dnsNotReadyTimedoutReason,
			},
		},
		{
			name: "no-op if already timed out",
			existingObjs: []runtime.Object{
				goodDNSZone(),
			},
			clusterDeployment: testclusterdeployment.Build(
				clusterDeploymentBase(),
				testclusterdeployment.WithCondition(hivev1.ClusterDeploymentCondition{
					Type:   hivev1.DNSNotReadyCondition,
					Status: corev1.ConditionTrue,
					Reason: dnsNotReadyTimedoutReason,
				}),
			),
			expectedDNSNotReadyCondition: &hivev1.ClusterDeploymentCondition{
				Type:   hivev1.DNSNotReadyCondition,
				Status: corev1.ConditionTrue,
				Reason: dnsNotReadyTimedoutReason,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			existingObjs := append(test.existingObjs, test.clusterDeployment)
			fakeClient := fake.NewClientBuilder().WithRuntimeObjects(existingObjs...).Build()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			mockRemoteClientBuilder := remoteclientmock.NewMockBuilder(mockCtrl)
			rcd := &ReconcileClusterDeployment{
				Client:                        fakeClient,
				scheme:                        scheme.Scheme,
				logger:                        log.WithField("controller", "clusterDeployment"),
				remoteClusterAPIClientBuilder: func(*hivev1.ClusterDeployment) remoteclient.Builder { return mockRemoteClientBuilder },
			}

			// act
			updated, result, err := rcd.ensureManagedDNSZone(test.clusterDeployment, rcd.logger)
			actualDNSNotReadyCondition := controllerutils.FindCondition(test.clusterDeployment.Status.Conditions, hivev1.DNSNotReadyCondition)

			// assert
			assert.Equal(t, test.expectedUpdated, updated, "Unexpected 'updated' return")
			assert.Equal(t, test.expectedResult, result, "Unexpected 'result' return")
			if test.expectedErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Tests that start off with an existing DNSZone may not want to bother checking that it still exists
			if test.expectedDNSZone != nil {
				actualDNSZone := &hivev1.DNSZone{}
				fakeClient.Get(context.TODO(), types.NamespacedName{Namespace: testNamespace, Name: controllerutils.DNSZoneName(testName)}, actualDNSZone)
				// Just assert the fields we care about. Otherwise we have to muck with e.g. typemeta
				assert.Equal(t, test.expectedDNSZone.Namespace, actualDNSZone.Namespace, "Unexpected DNSZone namespace")
				assert.Equal(t, test.expectedDNSZone.Name, actualDNSZone.Name, "Unexpected DNSZone name")
				// TODO: Add assertions for (which will require setting up the CD and goodDNSZone more thoroughly):
				// - ControllerReference
				// - Labels
				// - Zone
				// - LinkToParentDomain
				// - AWS CredentialsSecretRef, CredentialsAssumeRole, AdditionalTags, Region
			}

			// TODO: Test setDNSDelayMetric paths

			if actualDNSNotReadyCondition != nil {
				actualDNSNotReadyCondition.LastProbeTime = metav1.Time{}      // zero out so it won't be checked.
				actualDNSNotReadyCondition.LastTransitionTime = metav1.Time{} // zero out so it won't be checked.
				actualDNSNotReadyCondition.Message = ""                       // zero out so it won't be checked.
			}
			assert.Equal(t, test.expectedDNSNotReadyCondition, actualDNSNotReadyCondition, "Expected DNSZone DNSNotReady condition doesn't match returned condition")
		})
	}
}

func getProvisions(c client.Client) []*hivev1.ClusterProvision {
	provisionList := &hivev1.ClusterProvisionList{}
	if err := c.List(context.TODO(), provisionList); err != nil {
		return nil
	}
	provisions := make([]*hivev1.ClusterProvision, len(provisionList.Items))
	for i := range provisionList.Items {
		provisions[i] = &provisionList.Items[i]
	}
	sort.Slice(provisions, func(i, j int) bool { return provisions[i].Spec.Attempt < provisions[j].Spec.Attempt })
	return provisions
}

func testCompletedImageSetJob() *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      imageSetJobName,
			Namespace: testNamespace,
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{
				Type:   batchv1.JobComplete,
				Status: corev1.ConditionTrue,
			}},
		},
	}
}

func testCompletedFailedImageSetJob() *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      imageSetJobName,
			Namespace: testNamespace,
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{
				Type:    batchv1.JobFailed,
				Status:  corev1.ConditionTrue,
				Reason:  "ImagePullBackoff",
				Message: "The pod failed to start because one the containers did not start",
			}},
		},
	}
}

func dnsZoneBase() testdnszone.Option {
	return func(dnsZone *hivev1.DNSZone) {
		dnsZone.Name = controllerutils.DNSZoneName(testName)
		dnsZone.Namespace = testNamespace
	}
}

func clusterDeploymentBase() testclusterdeployment.Option {
	return func(clusterDeployment *hivev1.ClusterDeployment) {
		clusterDeployment.Namespace = testNamespace
		clusterDeployment.Name = testName
		clusterDeployment.Spec.Platform.AWS = &hivev1aws.Platform{}
	}
}

// testReleaseVerifier returns Verify true for only provided known digests.
type testReleaseVerifier struct {
	known sets.String
}

func (t testReleaseVerifier) Verify(ctx context.Context, releaseDigest string) error {
	if !t.known.Has(releaseDigest) {
		return fmt.Errorf("verification did not succeed")
	}
	return nil
}

func (testReleaseVerifier) Signatures() map[string][][]byte {
	return nil
}

func (testReleaseVerifier) Verifiers() map[string]openpgp.EntityList {
	return nil
}

func (testReleaseVerifier) AddStore(_ store.Store) {
}
