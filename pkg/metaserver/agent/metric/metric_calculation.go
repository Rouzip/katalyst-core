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

// for those metrics need extra calculation logic,
// we will put them in a separate file here
import (
	"github.com/kubewharf/katalyst-core/pkg/consts"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent/metric/malachite/cgroup"
)

func (m *MalachiteMetricsFetcher) processContainerMemBandwidth(podUID, containerName string, cgStats *cgroup.MalachiteCgroupInfo) {
	var (
		lastOCRReadDRAMs, _ = m.metricStore.GetContainerMetric(podUID, containerName, consts.MetricOCRReadDRAMsContainer)
		lastIMCWrites, _    = m.metricStore.GetContainerMetric(podUID, containerName, consts.MetricIMCWriteContainer)
		lastStoreAllIns, _  = m.metricStore.GetContainerMetric(podUID, containerName, consts.MetricStoreAllInsContainer)
		lastStoreIns, _     = m.metricStore.GetContainerMetric(podUID, containerName, consts.MetricStoreInsContainer)
		lastUpdateTime, _   = m.metricStore.GetContainerMetric(podUID, containerName, consts.MetricUpdateTimeContainer)
	)

	var readBandwidth, writeBandwidth float64
	var curOCRReadDRAMs, curIMCWrites, curStoreAllIns, curStoreIns, curUpdateTime float64
	if cgStats.CgroupType == "V1" {
		curOCRReadDRAMs = float64(cgStats.V1.Cpu.OCRReadDRAMs)
		curIMCWrites = float64(cgStats.V1.Cpu.IMCWrites)
		curStoreAllIns = float64(cgStats.V1.Cpu.StoreAllInstructions)
		curStoreIns = float64(cgStats.V1.Cpu.StoreInstructions)
		curUpdateTime = float64(cgStats.V1.Cpu.UpdateTime)
	} else if cgStats.CgroupType == "V2" {
		curOCRReadDRAMs = float64(cgStats.V2.Cpu.OCRReadDRAMs)
		curIMCWrites = float64(cgStats.V2.Cpu.IMCWrites)
		curStoreAllIns = float64(cgStats.V2.Cpu.StoreAllInstructions)
		curStoreIns = float64(cgStats.V2.Cpu.StoreInstructions)
		curUpdateTime = float64(cgStats.V2.Cpu.UpdateTime)
	}

	// read/write bandwidth calculation formula
	timediffSecs := curUpdateTime - lastUpdateTime
	if timediffSecs > 0 {
		readBytes := (curOCRReadDRAMs - lastOCRReadDRAMs) * 64
		readBandwidth = readBytes / (1024 * 1024 * timediffSecs)

		if curStoreAllIns > lastStoreIns {
			writeBytes := (curStoreIns - lastStoreIns) * (curIMCWrites - lastIMCWrites) * 64 / (curStoreAllIns - lastStoreAllIns)
			writeBandwidth = writeBytes / (1024 * 1024 * timediffSecs)
		}
	}

	m.metricStore.SetContainerMetric(podUID, containerName, consts.MetricMemBandwidthReadContainer, readBandwidth)
	m.metricStore.SetContainerMetric(podUID, containerName, consts.MetricMemBandwidthWriteContainer, writeBandwidth)
}
