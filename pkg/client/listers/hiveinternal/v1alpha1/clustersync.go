// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "github.com/openshift/hive/apis/hiveinternal/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// ClusterSyncLister helps list ClusterSyncs.
// All objects returned here must be treated as read-only.
type ClusterSyncLister interface {
	// List lists all ClusterSyncs in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.ClusterSync, err error)
	// ClusterSyncs returns an object that can list and get ClusterSyncs.
	ClusterSyncs(namespace string) ClusterSyncNamespaceLister
	ClusterSyncListerExpansion
}

// clusterSyncLister implements the ClusterSyncLister interface.
type clusterSyncLister struct {
	indexer cache.Indexer
}

// NewClusterSyncLister returns a new ClusterSyncLister.
func NewClusterSyncLister(indexer cache.Indexer) ClusterSyncLister {
	return &clusterSyncLister{indexer: indexer}
}

// List lists all ClusterSyncs in the indexer.
func (s *clusterSyncLister) List(selector labels.Selector) (ret []*v1alpha1.ClusterSync, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.ClusterSync))
	})
	return ret, err
}

// ClusterSyncs returns an object that can list and get ClusterSyncs.
func (s *clusterSyncLister) ClusterSyncs(namespace string) ClusterSyncNamespaceLister {
	return clusterSyncNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// ClusterSyncNamespaceLister helps list and get ClusterSyncs.
// All objects returned here must be treated as read-only.
type ClusterSyncNamespaceLister interface {
	// List lists all ClusterSyncs in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1alpha1.ClusterSync, err error)
	// Get retrieves the ClusterSync from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1alpha1.ClusterSync, error)
	ClusterSyncNamespaceListerExpansion
}

// clusterSyncNamespaceLister implements the ClusterSyncNamespaceLister
// interface.
type clusterSyncNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all ClusterSyncs in the indexer for a given namespace.
func (s clusterSyncNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.ClusterSync, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.ClusterSync))
	})
	return ret, err
}

// Get retrieves the ClusterSync from the indexer for a given namespace and name.
func (s clusterSyncNamespaceLister) Get(name string) (*v1alpha1.ClusterSync, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("clustersync"), name)
	}
	return obj.(*v1alpha1.ClusterSync), nil
}
