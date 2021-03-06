package kustomize

import (
	"os"
	"strings"
	"testing"

	testlogr "github.com/go-logr/logr/testing"
	objectbucket "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/kustomize/api/types"

	v1 "github.com/quay/quay-operator/apis/quay/v1"
)

var kustomizationForTests = []struct {
	name         string
	quayRegistry *v1.QuayRegistry
	expected     *types.Kustomization
	expectedErr  string
}{
	{
		"InvalidQuayRegistry",
		nil,
		nil,
		"given QuayRegistry should not be nil",
	},
	{
		"AllComponents",
		&v1.QuayRegistry{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					v1.SupportsObjectStorageAnnotation: "true",
				},
			},
			Spec: v1.QuayRegistrySpec{
				Components: []v1.Component{
					{Kind: "postgres", Managed: true},
					{Kind: "clair", Managed: true},
					{Kind: "redis", Managed: true},
					{Kind: "objectstorage", Managed: true},
					{Kind: "mirror", Managed: true},
				},
			},
		},
		&types.Kustomization{
			TypeMeta: types.TypeMeta{
				APIVersion: types.KustomizationVersion,
				Kind:       types.KustomizationKind,
			},
			Resources: []string{},
			Components: []string{
				"../components/postgres",
				"../components/clair",
				"../components/redis",
				"../components/objectstorage",
				"../components/mirror",
			},
			SecretGenerator: []types.SecretArgs{},
		},
		"",
	},
	{
		"ComponentImageOverrides",
		&v1.QuayRegistry{
			Spec: v1.QuayRegistrySpec{
				Components: []v1.Component{
					{Kind: "postgres", Managed: true},
					{Kind: "clair", Managed: true},
					{Kind: "redis", Managed: true},
				},
			},
		},
		&types.Kustomization{
			TypeMeta: types.TypeMeta{
				APIVersion: types.KustomizationVersion,
				Kind:       types.KustomizationKind,
			},
			Resources: []string{},
			Components: []string{
				"../components/postgres",
				"../components/clair",
				"../components/redis",
			},
			Images: []types.Image{
				{Name: "quay.io/projectquay/quay", NewName: "quay", Digest: "sha256:abc123"},
				{Name: "quay.io/projectquay/clair", NewName: "clair", Digest: "sha256:abc123"},
				{Name: "centos/redis-32-centos7", NewName: "redis", Digest: "sha256:abc123"},
				{Name: "centos/postgresql-10-centos7", NewName: "postgres", Digest: "sha256:abc123"},
			},
			SecretGenerator: []types.SecretArgs{},
		},
		"",
	},
}

func TestKustomizationFor(t *testing.T) {
	assert := assert.New(t)

	for _, test := range kustomizationForTests {
		if test.expected != nil {
			for _, img := range test.expected.Images {
				os.Setenv("RELATED_IMAGE_COMPONENT_"+strings.ToUpper(img.NewName), img.NewName+"@"+img.Digest)
			}
		}

		kustomization, err := KustomizationFor(test.quayRegistry, map[string][]byte{})

		if test.expectedErr != "" {
			assert.EqualError(err, test.expectedErr)
			assert.Nil(kustomization, test.name)
		} else {
			assert.NotNil(kustomization, test.name)

			assert.Equal(len(test.expected.Components), len(kustomization.Components), test.name)
			for _, expectedComponent := range test.expected.Components {
				assert.Contains(kustomization.Components, expectedComponent, test.name)
			}

			assert.Equal(len(test.expected.Images), len(kustomization.Images), test.name)
			for _, img := range test.expected.Images {
				assert.Contains(kustomization.Images, img, test.name)
			}
		}
	}
}

func TestFlattenSecret(t *testing.T) {
	assert := assert.New(t)

	config := map[string]interface{}{
		"ENTERPRISE_LOGO_URL": "/static/img/quay-horizontal-color.svg",
		"FEATURE_SUPER_USERS": true,
		"SERVER_HOSTNAME":     "quay-app.quay-enterprise",
	}

	secret := &corev1.Secret{
		Data: map[string][]byte{
			"config.yaml": encode(config),
			"ssl.key":     encode("abcd1234"),
			"clair.config.yaml": encode(map[string]interface{}{
				"FEATURE_SECURITY_SCANNER":     true,
				"SECURITY_SCANNER_V4_ENDPOINT": "http://quay-clair",
			}),
		},
	}

	flattenedSecret, err := flattenSecret(secret)

	assert.Nil(err)
	assert.Equal(2, len(flattenedSecret.Data))
	assert.NotNil(flattenedSecret.Data["config.yaml"])

	flattenedConfig := decode(flattenedSecret.Data["config.yaml"])
	for key, value := range config {
		assert.Equal(value, flattenedConfig.(map[string]interface{})[key])
	}
	assert.Equal(true, flattenedConfig.(map[string]interface{})["FEATURE_SECURITY_SCANNER"])
	assert.Equal("http://quay-clair", flattenedConfig.(map[string]interface{})["SECURITY_SCANNER_V4_ENDPOINT"])
}

var quayComponents = map[string][]runtime.Object{
	"base": {
		&rbac.Role{ObjectMeta: metav1.ObjectMeta{Name: "quay-serviceaccount"}},
		&rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "quay-secret-writer"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "quay-app"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "quay-app-upgrade"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "quay-config-editor"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "quay-app"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "quay-config-editor"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "quay-config-secret"}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cluster-service-ca"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "quay-config-editor-credentials"}},
		// &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "quay-registry-managed-secret-keys"}},
	},
	"clair": {
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "clair-config-secret"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "clair"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "clair"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "clair-postgres"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "clair-postgres"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "clair-postgres"}},
	},
	"postgres": {
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "postgres-bootstrap"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "quay-database"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "quay-database"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "quay-database"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "quay-database-init"}},
	},
	"redis": {
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "quay-redis"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "quay-redis"}},
	},
	"objectstorage": {
		&objectbucket.ObjectBucketClaim{ObjectMeta: metav1.ObjectMeta{Name: "quay-datastorage"}},
	},
	"route": {
		// TODO(alecmerdler): Import OpenShift `Route` API struct
	},
	"mirror": {
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "quay-mirror"}},
	},
}

func withComponents(components []string) []runtime.Object {
	selectedComponents := []runtime.Object{}
	for _, component := range components {
		selectedComponents = append(selectedComponents, quayComponents[component]...)
	}

	return selectedComponents
}

var inflateTests = []struct {
	name         string
	quayRegistry *v1.QuayRegistry
	configBundle *corev1.Secret
	expected     []runtime.Object
	expectedErr  error
}{
	{
		"AllComponentsManagedExplicit",
		&v1.QuayRegistry{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					v1.SupportsObjectStorageAnnotation: "true",
				},
			},
			Spec: v1.QuayRegistrySpec{
				Components: []v1.Component{
					{Kind: "postgres", Managed: true},
					{Kind: "clair", Managed: true},
					{Kind: "redis", Managed: true},
					{Kind: "objectstorage", Managed: true},
					{Kind: "mirror", Managed: true},
				},
			},
		},
		&corev1.Secret{
			Data: map[string][]byte{
				"config.yaml": encode(map[string]interface{}{"SERVER_HOSTNAME": "quay.io"}),
			},
		},
		withComponents([]string{"base", "clair", "postgres", "redis", "objectstorage", "mirror"}),
		nil,
	},
	{
		"AllComponentsUnmanaged",
		&v1.QuayRegistry{
			Spec: v1.QuayRegistrySpec{
				Components: []v1.Component{
					{Kind: "postgres", Managed: false},
					{Kind: "clair", Managed: false},
					{Kind: "redis", Managed: false},
					{Kind: "objectstorage", Managed: false},
					{Kind: "mirror", Managed: false},
				},
			},
		},
		&corev1.Secret{
			Data: map[string][]byte{
				"config.yaml": encode(map[string]interface{}{"SERVER_HOSTNAME": "quay.io"}),
			},
		},
		withComponents([]string{"base"}),
		nil,
	},
	{
		"SomeComponentsUnmanaged",
		&v1.QuayRegistry{
			Spec: v1.QuayRegistrySpec{
				Components: []v1.Component{
					{Kind: "postgres", Managed: true},
					{Kind: "clair", Managed: true},
					{Kind: "redis", Managed: false},
					{Kind: "objectstorage", Managed: false},
					{Kind: "mirror", Managed: true},
				},
			},
		},
		&corev1.Secret{
			Data: map[string][]byte{
				"config.yaml": encode(map[string]interface{}{"SERVER_HOSTNAME": "quay.io"}),
			},
		},
		withComponents([]string{"base", "postgres", "clair", "mirror"}),
		nil,
	},
	{
		"CurrentVersion",
		&v1.QuayRegistry{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					v1.SupportsObjectStorageAnnotation: "true",
				},
			},
			Spec: v1.QuayRegistrySpec{
				Components: []v1.Component{
					{Kind: "postgres", Managed: true},
					{Kind: "clair", Managed: true},
					{Kind: "redis", Managed: true},
					{Kind: "objectstorage", Managed: true},
					{Kind: "mirror", Managed: true},
				},
			},
			Status: v1.QuayRegistryStatus{
				CurrentVersion: v1.QuayVersionCurrent,
			},
		},
		&corev1.Secret{
			Data: map[string][]byte{
				"config.yaml": encode(map[string]interface{}{"SERVER_HOSTNAME": "quay.io"}),
			},
		},
		withComponents([]string{"base", "clair", "postgres", "redis", "objectstorage", "mirror"}),
		nil,
	},
}

func TestInflate(t *testing.T) {
	assert := assert.New(t)

	log := testlogr.TestLogger{}

	for _, test := range inflateTests {
		pieces, err := Inflate(test.quayRegistry, test.configBundle, nil, log)

		assert.NotNil(pieces, test.name)
		assert.Equal(len(test.expected), len(pieces), test.name)
		assert.Nil(err, test.name)

		for _, obj := range pieces {
			objectMeta, _ := meta.Accessor(obj)

			assert.Contains(objectMeta.GetName(), test.quayRegistry.GetName()+"-", test.name)
			if !strings.Contains(objectMeta.GetName(), "managed-secret-keys") {
				assert.Equal(string(v1.QuayVersionCurrent), objectMeta.GetAnnotations()["quay-version"], test.name)
			}
		}
	}
}
