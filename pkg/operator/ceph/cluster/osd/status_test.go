/*
Copyright 2018 The Rook Authors. All rights reserved.

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
package osd

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/rook/rook/pkg/clusterd"
	cephclient "github.com/rook/rook/pkg/daemon/ceph/client"
	cephver "github.com/rook/rook/pkg/operator/ceph/version"
	"github.com/rook/rook/pkg/operator/k8sutil"
	exectest "github.com/rook/rook/pkg/util/exec/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
)

func TestOrchestrationStatus(t *testing.T) {
	ctx := context.TODO()
	clientset := fake.NewClientset()
	clusterInfo := &cephclient.ClusterInfo{
		Namespace:   "ns",
		CephVersion: cephver.Squid,
	}
	context := &clusterd.Context{Clientset: clientset, ConfigDir: "/var/lib/rook", Executor: &exectest.MockExecutor{}}
	spec := cephv1.ClusterSpec{}
	c := New(context, clusterInfo, spec, "myversion")
	ownerInfo := cephclient.NewMinimumOwnerInfoWithOwnerRef()
	kv := k8sutil.NewConfigMapKVStore(c.clusterInfo.Namespace, clientset, ownerInfo)
	nodeName := "mynode"
	cmName := fmt.Sprintf(orchestrationStatusMapName, nodeName)

	// status map should not exist yet
	_, err := c.context.Clientset.CoreV1().ConfigMaps(c.clusterInfo.Namespace).Get(ctx, cmName, metav1.GetOptions{})
	assert.True(t, errors.IsNotFound(err))

	// update the status map with some status
	status := OrchestrationStatus{Status: OrchestrationStatusOrchestrating, Message: "doing work"}
	UpdateNodeOrPVCStatus(ctx, kv, nodeName, status)

	// retrieve the status and verify it
	statusMap, err := c.context.Clientset.CoreV1().ConfigMaps(c.clusterInfo.Namespace).Get(ctx, cmName, metav1.GetOptions{})
	assert.Nil(t, err)
	assert.NotNil(t, statusMap)
	retrievedStatus := parseOrchestrationStatus(statusMap.Data)
	assert.NotNil(t, retrievedStatus)
	assert.Equal(t, status, *retrievedStatus)
}

func mockNodeOrchestrationCompletion(c *Cluster, nodeName string, statusMapWatcher *watch.RaceFreeFakeWatcher) {
	ctx := context.TODO()
	// if no valid osd node, don't need to check its status, return immediately
	if len(c.spec.Storage.Nodes) == 0 {
		return
	}
	for {
		// wait for the node's orchestration status to change to "starting"
		cmName := statusConfigMapName(nodeName)
		cm, err := c.context.Clientset.CoreV1().ConfigMaps(c.clusterInfo.Namespace).Get(ctx, cmName, metav1.GetOptions{})
		if err == nil {
			status := parseOrchestrationStatus(cm.Data)
			if status != nil && status.Status == OrchestrationStatusStarting {
				// the node has started orchestration, simulate its completion now by performing 2 tasks:
				// 1) update the config map manually (which doesn't trigger a watch event, see https://github.com/kubernetes/kubernetes/issues/54075#issuecomment-337298950)
				status = &OrchestrationStatus{
					OSDs: []OSDInfo{
						{
							ID:        1,
							UUID:      "000000-0000-00000001",
							Cluster:   "rook",
							CVMode:    "raw",
							BlockPath: "/dev/some/path",
						},
					},
					Status: OrchestrationStatusCompleted,
				}
				UpdateNodeOrPVCStatus(ctx, c.kv, nodeName, *status)

				// 2) call modify on the fake watcher so a watch event will get triggered
				s, _ := json.Marshal(status)
				cm.Data[orchestrationStatusKey] = string(s)
				statusMapWatcher.Modify(cm)
				break
			} else {
				logger.Debugf("waiting for node %s orchestration to start. status: %+v", nodeName, *status)
			}
		} else {
			logger.Warningf("failed to get node %s orchestration status, will try again: %+v", nodeName, err)
		}
		<-time.After(50 * time.Millisecond)
	}
}

func waitForOrchestrationCompletion(c *Cluster, nodeName string, startCompleted *bool) {
	ctx := context.TODO()
	for {
		if *startCompleted {
			break
		}
		cmName := statusConfigMapName(nodeName)
		cm, err := c.context.Clientset.CoreV1().ConfigMaps(c.clusterInfo.Namespace).Get(ctx, cmName, metav1.GetOptions{})
		if err == nil {
			status := parseOrchestrationStatus(cm.Data)
			if status != nil {
				logger.Debugf("start has not completed, status is %+v", status)
			}
		}
		<-time.After(50 * time.Millisecond)
	}
}

func TestPrepareStatusRetry(t *testing.T) {
	for _, pvcBacked := range []bool{false, true} {
		for _, succeeds := range []bool{false, true} {
			t.Run(fmt.Sprintf("pvc=%t/succeeds=%t", pvcBacked, succeeds), func(t *testing.T) {
				ctx := context.Background()
				clientset := fake.NewClientset()
				clusterInfo := &cephclient.ClusterInfo{
					Context: ctx, Namespace: "ns", CephVersion: cephver.Squid,
					OwnerInfo: cephclient.NewMinimumOwnerInfoWithOwnerRef(),
				}
				c := New(&clusterd.Context{Clientset: clientset}, clusterInfo, cephv1.ClusterSpec{}, "rook/rook:master")
				name := "retry-device"
				cmName := statusConfigMapName(name)
				config := c.newCreateConfig(c.newProvisionConfig(), sets.New(cmName), newExistenceListWithCapacity(1))
				errs := newProvisionErrors()
				job, err := clientset.BatchV1().Jobs("ns").Create(ctx, &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: provisionJobName(name), Namespace: "ns"},
					Status:     batchv1.JobStatus{Active: 1},
				}, metav1.CreateOptions{})
				require.NoError(t, err)

				creates := 0
				oldNode, oldPVC := createDaemonOnNodeFunc, createDaemonOnPVCFunc
				t.Cleanup(func() { createDaemonOnNodeFunc, createDaemonOnPVCFunc = oldNode, oldPVC })
				create := func(_ *Cluster, osd *OSDInfo, nodeOrPVC string, _ *provisionConfig) error {
					creates++
					assert.Equal(t, 12, osd.ID)
					assert.Equal(t, name, nodeOrPVC)
					return nil
				}
				createDaemonOnNodeFunc, createDaemonOnPVCFunc = create, create

				process := func(status OrchestrationStatus) {
					UpdateNodeOrPVCStatus(ctx, c.kv, name, status)
					cm, err := clientset.CoreV1().ConfigMaps("ns").Get(ctx, cmName, metav1.GetOptions{})
					require.NoError(t, err)
					c.createOSDsForStatusMap(cm, config, errs)
				}
				process(OrchestrationStatus{Status: OrchestrationStatusFailed, PvcBackedOSD: pvcBacked, Message: "attempt interrupted"})
				assert.False(t, config.doneCreating(), "an attempt failure must not finish provisioning")
				assert.Zero(t, errs.len())
				_, err = clientset.CoreV1().ConfigMaps("ns").Get(ctx, cmName, metav1.GetOptions{})
				require.NoError(t, err, "retain the status while the Job can retry")

				// Backoff between attempts can have no active pod without being terminal.
				job.Status.Active = 0
				job.Status.Failed = 1
				job, err = clientset.BatchV1().Jobs("ns").UpdateStatus(ctx, job, metav1.UpdateOptions{})
				require.NoError(t, err)
				require.NoError(t, c.recheckPendingOSDStatuses(config, errs))
				assert.False(t, config.doneCreating())

				if succeeds {
					// A successful retry must be accepted even without an intermediate status event.
					completed := OrchestrationStatus{Status: OrchestrationStatusCompleted, PvcBackedOSD: pvcBacked, OSDs: []OSDInfo{{ID: 12}}}
					process(completed)
					assert.Equal(t, 1, creates)
					assert.Zero(t, errs.len())
					process(completed)
					assert.Equal(t, 1, creates, "duplicate success must not create OSDs again")
				} else {
					// Only the Job changes when retries are exhausted; no ConfigMap event arrives.
					job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}
					_, err = clientset.BatchV1().Jobs("ns").UpdateStatus(ctx, job, metav1.UpdateOptions{})
					require.NoError(t, err)
					require.NoError(t, c.recheckPendingOSDStatuses(config, errs))
					assert.Equal(t, 1, errs.len())
					assert.Zero(t, creates)
				}
				assert.True(t, config.doneCreating())
				_, err = clientset.CoreV1().ConfigMaps("ns").Get(ctx, cmName, metav1.GetOptions{})
				assert.True(t, errors.IsNotFound(err), "processed terminal results should be cleaned up")
			})
		}
	}
}
