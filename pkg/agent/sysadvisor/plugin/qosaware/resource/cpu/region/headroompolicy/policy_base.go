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

package headroompolicy

import (
	"github.com/kubewharf/katalyst-core/pkg/agent/sysadvisor/metacache"
	"github.com/kubewharf/katalyst-core/pkg/agent/sysadvisor/types"
	"github.com/kubewharf/katalyst-core/pkg/metaserver"
	"github.com/kubewharf/katalyst-core/pkg/metrics"
)

type PolicyBase struct {
	regionName string

	headroom   float64
	podSet     types.PodSet
	essentials types.ResourceEssentials

	metaReader metacache.MetaReader
	metaServer *metaserver.MetaServer
	emitter    metrics.MetricEmitter
}

func NewPolicyBase(regionName string, metaReader metacache.MetaReader, metaServer *metaserver.MetaServer,
	emitter metrics.MetricEmitter) *PolicyBase {
	cp := &PolicyBase{
		regionName: regionName,
		podSet:     make(types.PodSet),

		metaReader: metaReader,
		metaServer: metaServer,
		emitter:    emitter,
	}
	return cp
}

func (p *PolicyBase) SetPodSet(podSet types.PodSet) {
	p.podSet = podSet.Clone()
}

func (p *PolicyBase) SetEssentials(essentials types.ResourceEssentials) {
	p.essentials = essentials
}
