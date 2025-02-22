/*
Copyright 2022 The Katalyst Authors.

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

package metric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent/metric/malachite/cgroup"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent/metric/malachite/system"
	"github.com/kubewharf/katalyst-core/pkg/metrics"
	"github.com/kubewharf/katalyst-core/pkg/util/machine"
	"github.com/kubewharf/katalyst-core/pkg/util/metric"
)

func Test_noneExistMetricsFetcher(t *testing.T) {
	var err error
	implement := NewMalachiteMetricsFetcher(metrics.DummyMetrics{})

	fakeSystemCompute := &system.SystemComputeData{
		CPU: []system.CPU{
			{
				Name: "CPU1111",
			},
		},
	}
	fakeSystemMemory := &system.SystemMemoryData{
		Numa: []system.Numa{
			{},
		},
	}
	fakeSystemIO := &system.SystemDiskIoData{
		DiskIo: []system.DiskIo{
			{},
		},
	}
	fakeCgroupInfoV1 := &cgroup.MalachiteCgroupInfo{
		CgroupType: "V1",
		V1: &cgroup.MalachiteCgroupV1Info{
			Memory:    &cgroup.MemoryCgDataV1{},
			Blkio:     &cgroup.BlkIOCgDataV1{},
			NetCls:    &cgroup.NetClsCgData{},
			PerfEvent: &cgroup.PerfEventData{},
			CpuSet:    &cgroup.CPUSetCgDataV1{},
			Cpu:       &cgroup.CPUCgDataV1{},
		},
	}
	fakeCgroupInfoV2 := &cgroup.MalachiteCgroupInfo{
		CgroupType: "V2",
		V2: &cgroup.MalachiteCgroupV2Info{
			Memory:    &cgroup.MemoryCgDataV2{},
			Blkio:     &cgroup.BlkIOCgDataV2{},
			NetCls:    &cgroup.NetClsCgData{},
			PerfEvent: &cgroup.PerfEventData{},
			CpuSet:    &cgroup.CPUSetCgDataV2{},
			Cpu:       &cgroup.CPUCgDataV2{},
		},
	}

	implement.(*MalachiteMetricsFetcher).processSystemComputeData(fakeSystemCompute)
	implement.(*MalachiteMetricsFetcher).processSystemMemoryData(fakeSystemMemory)
	implement.(*MalachiteMetricsFetcher).processSystemIOData(fakeSystemIO)
	implement.(*MalachiteMetricsFetcher).processSystemNumaData(fakeSystemMemory)
	implement.(*MalachiteMetricsFetcher).processSystemCPUComputeData(fakeSystemCompute)

	implement.(*MalachiteMetricsFetcher).processCgroupCPUData("pod-not-exist", "container-not-exist", fakeCgroupInfoV1)
	implement.(*MalachiteMetricsFetcher).processCgroupMemoryData("pod-not-exist", "container-not-exist", fakeCgroupInfoV1)
	implement.(*MalachiteMetricsFetcher).processCgroupBlkIOData("pod-not-exist", "container-not-exist", fakeCgroupInfoV1)
	implement.(*MalachiteMetricsFetcher).processCgroupNetData("pod-not-exist", "container-not-exist", fakeCgroupInfoV1)
	implement.(*MalachiteMetricsFetcher).processCgroupPerfData("pod-not-exist", "container-not-exist", fakeCgroupInfoV1)
	implement.(*MalachiteMetricsFetcher).processCgroupPerNumaMemoryData("pod-not-exist", "container-not-exist", fakeCgroupInfoV1)

	implement.(*MalachiteMetricsFetcher).processCgroupCPUData("pod-not-exist", "container-not-exist", fakeCgroupInfoV2)
	implement.(*MalachiteMetricsFetcher).processCgroupMemoryData("pod-not-exist", "container-not-exist", fakeCgroupInfoV2)
	implement.(*MalachiteMetricsFetcher).processCgroupBlkIOData("pod-not-exist", "container-not-exist", fakeCgroupInfoV2)
	implement.(*MalachiteMetricsFetcher).processCgroupNetData("pod-not-exist", "container-not-exist", fakeCgroupInfoV2)
	implement.(*MalachiteMetricsFetcher).processCgroupPerfData("pod-not-exist", "container-not-exist", fakeCgroupInfoV2)
	implement.(*MalachiteMetricsFetcher).processCgroupPerNumaMemoryData("pod-not-exist", "container-not-exist", fakeCgroupInfoV2)

	_, err = implement.GetNodeMetric("test-not-exist")
	if err == nil {
		t.Errorf("GetNode() error = %v, wantErr not nil", err)
		return
	}

	_, err = implement.GetNumaMetric(1, "test-not-exist")
	if err == nil {
		t.Errorf("GetNode() error = %v, wantErr not nil", err)
		return
	}

	_, err = implement.GetDeviceMetric("device-not-exist", "test-not-exist")
	if err == nil {
		t.Errorf("GetNode() error = %v, wantErr not nil", err)
		return
	}

	_, err = implement.GetCPUMetric(1, "test-not-exist")
	if err == nil {
		t.Errorf("GetNode() error = %v, wantErr not nil", err)
		return
	}

	_, err = implement.GetContainerMetric("pod-not-exist", "container-not-exist", "test-not-exist")
	if err == nil {
		t.Errorf("GetNode() error = %v, wantErr not nil", err)
		return
	}

	_, err = implement.GetContainerNumaMetric("pod-not-exist", "container-not-exist", "", "test-not-exist")
	if err == nil {
		t.Errorf("GetContainerNuma() error = %v, wantErr not nil", err)
		return
	}
}

func Test_notifySystem(t *testing.T) {
	f := NewMalachiteMetricsFetcher(metrics.DummyMetrics{})

	rChan := make(chan NotifiedResponse, 20)
	f.RegisterNotifier(MetricsScopeNode, NotifiedRequest{
		MetricName: "test-node-metric",
	}, rChan)
	f.RegisterNotifier(MetricsScopeNuma, NotifiedRequest{
		MetricName: "test-numa-metric",
		NumaID:     1,
	}, rChan)
	f.RegisterNotifier(MetricsScopeCPU, NotifiedRequest{
		MetricName: "test-cpu-metric",
		CoreID:     2,
	}, rChan)
	f.RegisterNotifier(MetricsScopeDevice, NotifiedRequest{
		MetricName: "test-device-metric",
		DeviceID:   "test-device",
	}, rChan)
	f.RegisterNotifier(MetricsScopeContainer, NotifiedRequest{
		MetricName:    "test-container-metric",
		PodUID:        "test-pod",
		ContainerName: "test-container",
	}, rChan)
	f.RegisterNotifier(MetricsScopeContainer, NotifiedRequest{
		MetricName:    "test-container-numa-metric",
		PodUID:        "test-pod",
		ContainerName: "test-container",
		NumaNode:      "3",
	}, rChan)

	m := f.(*MalachiteMetricsFetcher)
	m.metricStore.SetNodeMetric("test-node-metric", 34)
	m.metricStore.SetNumaMetric(1, "test-numa-metric", 56)
	m.metricStore.SetCPUMetric(2, "test-cpu-metric", 78)
	m.metricStore.SetDeviceMetric("test-device", "test-device-metric", 91)
	m.metricStore.SetContainerMetric("test-pod", "test-container", "test-container-metric", 91)
	m.metricStore.SetContainerNumaMetric("test-pod", "test-container", "3", "test-container-numa-metric", 75)

	go func() {
		for {
			select {
			case response := <-rChan:
				switch response.Req.MetricName {
				case "test-node-metric":
					assert.Equal(t, response.Result, 34)
				case "test-numa-metric":
					assert.Equal(t, response.Result, 56)
				case "test-cpu-metric":
					assert.Equal(t, response.Result, 78)
				case "test-device-metric":
					assert.Equal(t, response.Result, 91)
				case "test-container-metric":
					assert.Equal(t, response.Result, 91)
				case "test-container-numa-metric":
					assert.Equal(t, response.Result, 75)
				}
			}
		}
	}()

	time.Sleep(time.Second * 10)
}

func TestStore_Aggregate(t *testing.T) {
	f := NewMalachiteMetricsFetcher(metrics.DummyMetrics{}).(*MalachiteMetricsFetcher)

	pod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: "pod1",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "container1",
				},
				{
					Name: "container2",
				},
			},
		},
	}
	pod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: "pod2",
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "container3",
				},
			},
		},
	}
	pod3 := &v1.Pod{}

	f.metricStore.SetContainerMetric("pod1", "container1", "test-pod-metric", 1.0)
	f.metricStore.SetContainerMetric("pod1", "container2", "test-pod-metric", 1.0)
	f.metricStore.SetContainerMetric("pod2", "container3", "test-pod-metric", 1.0)
	sum := f.AggregatePodMetric([]*v1.Pod{pod1, pod2, pod3}, "test-pod-metric", metric.AggregatorSum, metric.DefaultContainerMetricFilter)
	assert.Equal(t, float64(3), sum)
	avg := f.AggregatePodMetric([]*v1.Pod{pod1, pod2, pod3}, "test-pod-metric", metric.AggregatorAvg, metric.DefaultContainerMetricFilter)
	assert.Equal(t, float64(1.5), avg)

	f.metricStore.SetContainerNumaMetric("pod1", "container1", "0", "test-pod-numa-metric", 1.0)
	f.metricStore.SetContainerNumaMetric("pod1", "container2", "0", "test-pod-numa-metric", 1.0)
	f.metricStore.SetContainerNumaMetric("pod1", "container2", "1", "test-pod-numa-metric", 1.0)
	f.metricStore.SetContainerNumaMetric("pod2", "container3", "0", "test-pod-numa-metric", 1.0)
	f.metricStore.SetContainerNumaMetric("pod2", "container3", "1", "test-pod-numa-metric", 1.0)
	f.metricStore.SetContainerNumaMetric("pod2", "container3", "1", "test-pod-numa-metric", 1.0)
	sum = f.AggregatePodNumaMetric([]*v1.Pod{pod1, pod2, pod3}, "0", "test-pod-numa-metric", metric.AggregatorSum, metric.DefaultContainerMetricFilter)
	assert.Equal(t, float64(3), sum)
	avg = f.AggregatePodNumaMetric([]*v1.Pod{pod1, pod2, pod3}, "0", "test-pod-numa-metric", metric.AggregatorAvg, metric.DefaultContainerMetricFilter)
	assert.Equal(t, float64(1.5), avg)
	sum = f.AggregatePodNumaMetric([]*v1.Pod{pod1, pod2, pod3}, "1", "test-pod-numa-metric", metric.AggregatorSum, metric.DefaultContainerMetricFilter)
	assert.Equal(t, float64(2), sum)
	avg = f.AggregatePodNumaMetric([]*v1.Pod{pod1, pod2, pod3}, "1", "test-pod-numa-metric", metric.AggregatorAvg, metric.DefaultContainerMetricFilter)
	assert.Equal(t, float64(1), avg)

	f.metricStore.SetCPUMetric(1, "test-cpu-metric", 1.0)
	f.metricStore.SetCPUMetric(1, "test-cpu-metric", 2.0)
	f.metricStore.SetCPUMetric(2, "test-cpu-metric", 1.0)
	f.metricStore.SetCPUMetric(0, "test-cpu-metric", 1.0)
	sum = f.AggregateCoreMetric(machine.NewCPUSet(0, 1, 2, 3), "test-cpu-metric", metric.AggregatorSum)
	assert.Equal(t, float64(4), sum)
	avg = f.AggregateCoreMetric(machine.NewCPUSet(0, 1, 2, 3), "test-cpu-metric", metric.AggregatorAvg)
	assert.Equal(t, float64(4/3.), avg)
}
