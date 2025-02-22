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

package cpu

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/klog/v2"

	"github.com/kubewharf/katalyst-api/pkg/consts"
	"github.com/kubewharf/katalyst-core/pkg/agent/qrm-plugins/cpu/dynamicpolicy/cpuadvisor"
	"github.com/kubewharf/katalyst-core/pkg/agent/qrm-plugins/cpu/dynamicpolicy/state"
	"github.com/kubewharf/katalyst-core/pkg/agent/sysadvisor/metacache"
	"github.com/kubewharf/katalyst-core/pkg/agent/sysadvisor/plugin/qosaware/resource/cpu/region"
	"github.com/kubewharf/katalyst-core/pkg/agent/sysadvisor/plugin/qosaware/resource/cpu/region/headroompolicy"
	"github.com/kubewharf/katalyst-core/pkg/agent/sysadvisor/plugin/qosaware/resource/cpu/region/provisionpolicy"
	"github.com/kubewharf/katalyst-core/pkg/agent/sysadvisor/types"
	"github.com/kubewharf/katalyst-core/pkg/config"
	"github.com/kubewharf/katalyst-core/pkg/metaserver"
	"github.com/kubewharf/katalyst-core/pkg/metrics"
	"github.com/kubewharf/katalyst-core/pkg/util/general"
	"github.com/kubewharf/katalyst-core/pkg/util/machine"
)

func init() {
	provisionpolicy.RegisterInitializer(types.CPUProvisionPolicyCanonical, provisionpolicy.NewPolicyCanonical)
	headroompolicy.RegisterInitializer(types.CPUHeadroomPolicyCanonical, headroompolicy.NewPolicyCanonical)
}

// todo:
// 1. Support dedicated with numa binding non-exclusive containers
// 2. Support shared cores containers with different cpu enhancement
// 3. Isolate bursting containers to isolation regions

const (
	startUpPeriod time.Duration = 30 * time.Second

	regionNameSeparator = "-"
)

// InternalCalculationResult conveys minimal calculation result to cpu server
type InternalCalculationResult struct {
	PoolEntries map[string]map[int]resource.Quantity // map[poolName][numaId]cores
}

// cpuResourceAdvisor is the entrance of updating cpu resource provision advice for
// all qos regions, and merging them into cpu provision result to notify cpu server.
// Smart algorithms and calculators could be adopted to give accurate realtime resource
// provision hint for each region.
type cpuResourceAdvisor struct {
	conf      *config.Configuration
	extraConf interface{}

	recvCh      chan struct{}
	sendCh      chan InternalCalculationResult
	startTime   time.Time
	systemNumas machine.CPUSet

	regionMap map[string]region.QoSRegion // map[regionName]region

	nonBindingNumas machine.CPUSet // numas without numa binding pods
	mutex           sync.RWMutex

	metaCache  metacache.MetaCache
	metaServer *metaserver.MetaServer
	emitter    metrics.MetricEmitter
}

// NewCPUResourceAdvisor returns a cpuResourceAdvisor instance
func NewCPUResourceAdvisor(conf *config.Configuration, extraConf interface{}, metaCache metacache.MetaCache,
	metaServer *metaserver.MetaServer, emitter metrics.MetricEmitter) *cpuResourceAdvisor {

	cra := &cpuResourceAdvisor{
		recvCh:      make(chan struct{}),
		sendCh:      make(chan InternalCalculationResult),
		startTime:   time.Now(),
		systemNumas: metaServer.CPUDetails.NUMANodes(),

		regionMap: make(map[string]region.QoSRegion),

		nonBindingNumas: machine.NewCPUSet(),

		conf:      conf,
		extraConf: extraConf,

		metaCache:  metaCache,
		metaServer: metaServer,
		emitter:    emitter,
	}

	return cra
}

func (cra *cpuResourceAdvisor) Run(ctx context.Context) {
	for {
		select {
		case <-cra.recvCh:
			klog.Infof("[qosaware-cpu] receive update trigger from cpu server")
			cra.update()
		case <-ctx.Done():
			return
		}
	}
}

func (cra *cpuResourceAdvisor) GetChannels() (interface{}, interface{}) {
	return cra.recvCh, cra.sendCh
}

func (cra *cpuResourceAdvisor) GetHeadroom() (resource.Quantity, error) {
	cra.mutex.RLock()
	defer cra.mutex.RUnlock()

	reservePoolSize, ok := cra.metaCache.GetPoolSize(state.PoolNameReserve)
	if !ok {
		return resource.Quantity{}, fmt.Errorf("reserve pool not exist")
	}

	// Return maximum available resource value as headroom when no region exists
	if len(cra.regionMap) <= 0 {
		return *resource.NewQuantity(int64(cra.metaServer.NumCPUs-reservePoolSize), resource.DecimalSI), nil
	}

	hasShareRegion := false
	totalHeadroom := resource.NewQuantity(0, resource.DecimalSI)
	for _, r := range cra.regionMap {
		headroom, err := r.GetHeadroom()
		if err != nil {
			return headroom, err
		}
		if r.Type() == types.QoSRegionTypeShare {
			hasShareRegion = true
		}
		// FIXME: is it reasonable to simply add headroom together?
		totalHeadroom.Add(headroom)
	}

	// Add headroom of numas without numa binding pods if there is no share region
	if !hasShareRegion {
		reservePoolSizeOfNonBindingNumas := int64(math.Ceil(float64(reservePoolSize*cra.nonBindingNumas.Size()) / float64(cra.metaServer.NumNUMANodes)))
		headroomOfNonBindingNumas := resource.NewQuantity(int64(cra.nonBindingNumas.Size()*cra.metaServer.CPUsPerNuma())-reservePoolSizeOfNonBindingNumas, resource.DecimalSI)
		totalHeadroom.Add(*headroomOfNonBindingNumas)
	}

	return *totalHeadroom, nil
}

// update works in a monolith way to maintain lifecycle and trigger update actions for all regions;
// todo: re-consider whether it's efficient or we should make start individual goroutine for each region
func (cra *cpuResourceAdvisor) update() {
	cra.mutex.Lock()
	defer cra.mutex.Unlock()

	// check if essential pool info exists. skip update if not in which case sysadvisor
	// is ignorant of pools and containers
	reservePoolInfo, ok := cra.metaCache.GetPoolInfo(state.PoolNameReserve)
	if !ok || reservePoolInfo == nil {
		klog.Warningf("[qosaware-cpu] skip update: reserve pool not exist")
		return
	}

	// assign containers to regions
	if err := cra.assignContainersToRegions(); err != nil {
		klog.Errorf("[qosaware-cpu] assign containers to regions failed: %v", err)
		return
	}
	klog.Infof("[qosaware-cpu] region map: %v", general.ToString(cra.regionMap))

	// run an episode of provision policy update for each region
	for _, r := range cra.regionMap {
		regionNumas := r.GetBindingNumas()

		// calculate region max available cpu limit,
		// which equals the number of cpus in region numas
		regionCPULimit := regionNumas.Size() * cra.metaServer.CPUsPerNuma()

		// calculate region reserve pool size value, which equals the cpuset intersection
		// size between region numas and node reserve pool
		regionReservePoolSize := 0
		for _, numaID := range regionNumas.ToSliceInt() {
			if cpuset, ok := reservePoolInfo.TopologyAwareAssignments[numaID]; ok {
				regionReservePoolSize += cpuset.Size()
			}
		}

		reserved := cra.conf.ReclaimedResourceConfiguration.ReservedResourceForAllocate()[v1.ResourceCPU]
		reservedForAllocate := reserved.Value()

		// calculate region reserved for allocate, which equals average per numa reserved
		// value times the number of numa nodes
		regionReservedForAllocate := int(math.Ceil(float64(int(reservedForAllocate)*regionNumas.Size()) /
			float64(cra.metaServer.NumNUMANodes)))

		r.SetEssentials(types.ResourceEssentials{
			Total:               regionCPULimit,
			ReservePoolSize:     regionReservePoolSize,
			ReservedForAllocate: regionReservedForAllocate,
			EnableReclaim:       cra.conf.ReclaimedResourceConfiguration.EnableReclaim(),
		})

		r.TryUpdateProvision()
	}

	// sync region information to metacache
	regionEntries, err := cra.assembleRegionEntries()
	if err != nil {
		klog.Errorf("[qosaware-cpu] assemble region entries failed: %v", err)
		return
	}
	_ = cra.metaCache.UpdateRegionEntries(regionEntries)

	// skip notifying cpu server during startup
	if time.Now().Before(cra.startTime.Add(startUpPeriod)) {
		klog.Infof("[qosaware-cpu] skip notifying cpu server: starting up")
		return
	}

	// assemble provision result from each region
	provision, err := cra.assembleProvision()
	if err != nil {
		klog.Errorf("[qosaware-cpu] assemble provision failed: %v", err)
		return
	}

	// notify cpu server about provision result
	cra.sendCh <- provision
	klog.Infof("[qosaware-cpu] notify cpu server: %+v", provision)

	// update headroom policy. do this after updating provision because headroom policy
	// may need the latest region provision result from metacache.
	for _, r := range cra.regionMap {
		r.TryUpdateHeadroom()
	}
}

// assignContainersToRegions re-construct regions every time (instead of an incremental way),
// and this requires metaCache to ensure data integrity
func (cra *cpuResourceAdvisor) assignContainersToRegions() error {
	var errList []error

	// Clear containers for all regions
	for _, r := range cra.regionMap {
		r.Clear()
	}

	// Sync containers
	f := func(podUID string, containerName string, ci *types.ContainerInfo) bool {
		regions, err := cra.assignToRegions(ci)
		if err != nil {
			errList = append(errList, err)
		}
		if regions == nil {
			return true
		}

		for _, r := range regions {
			if err := r.AddContainer(ci); err != nil {
				errList = append(errList, err)
				return true
			}
			// region may be set in regionMap for multiple times, and it is reentrant
			cra.regionMap[r.Name()] = r
		}

		cra.setContainerRegions(ci, regions)
		// dedicated pool is not existed in metaCache.poolEntries
		if ci.OwnerPoolName == state.PoolNameDedicated {
			return true
		}
		if err := cra.setPoolRegions(ci.OwnerPoolName, regions); err != nil {
			errList = append(errList, err)
			return true
		}

		return true
	}
	cra.metaCache.RangeAndUpdateContainer(f)

	cra.gc()
	cra.updateNonBindingNumas()

	return errors.NewAggregate(errList)
}

// assignToRegions returns the region list for the given container;
// may need to construct region structures if they don't exist.
func (cra *cpuResourceAdvisor) assignToRegions(ci *types.ContainerInfo) ([]region.QoSRegion, error) {
	if ci.QoSLevel == consts.PodAnnotationQoSLevelSharedCores {
		// Assign shared cores container. Focus on pool.
		regions, err := cra.getPoolRegions(ci.OwnerPoolName)
		if err != nil {
			return nil, err
		}
		if len(regions) > 0 {
			return regions, nil
		}

		name := string(types.QoSRegionTypeShare) + regionNameSeparator + string(uuid.NewUUID())
		r := region.NewQoSRegionShare(name, ci.OwnerPoolName, cra.conf, cra.extraConf, cra.metaCache, cra.metaServer, cra.emitter)

		return []region.QoSRegion{r}, nil

	} else if ci.IsNumaBinding() {
		// Assign dedicated cores numa exclusive containers. Focus on container.
		regions, err := cra.getContainerRegions(ci)
		if err != nil {
			return nil, err
		}
		if len(regions) > 0 {
			return regions, nil
		}

		// Create regions by numa node
		for numaID := range ci.TopologyAwareAssignments {
			name := string(types.QoSRegionTypeDedicatedNumaExclusive) + regionNameSeparator + string(uuid.NewUUID())
			r := region.NewQoSRegionDedicatedNumaExclusive(name, ci.OwnerPoolName, cra.conf, numaID, cra.extraConf, cra.metaCache, cra.metaServer, cra.emitter)
			regions = append(regions, r)
		}

		return regions, nil
	}

	return nil, nil
}

// updateNonBindingNumas updates numas without numa binding pods
// non-binding-numa = system-numa - dedicated-exclusive-numa
func (cra *cpuResourceAdvisor) updateNonBindingNumas() {
	cra.nonBindingNumas = cra.systemNumas

	for _, r := range cra.regionMap {
		if r.Type() == types.QoSRegionTypeDedicatedNumaExclusive {
			cra.nonBindingNumas = cra.nonBindingNumas.Difference(r.GetBindingNumas())
		}
	}

	// Set binding numas for non numa binding regions
	for _, r := range cra.regionMap {
		if r.Type() == types.QoSRegionTypeShare {
			r.SetBindingNumas(cra.nonBindingNumas)
		}
	}
}

// assembleRegionEntries generates region entries based on region map
func (cra *cpuResourceAdvisor) assembleRegionEntries() (types.RegionEntries, error) {
	entries := make(types.RegionEntries)

	for regionName, r := range cra.regionMap {
		controlKnobMap, err := r.GetProvision()
		if err != nil {
			return nil, err
		}

		regionInfo := &types.RegionInfo{
			RegionType:     r.Type(),
			BindingNumas:   r.GetBindingNumas(),
			ControlKnobMap: controlKnobMap,
		}
		regionInfo.HeadroomPolicyTopPriority, regionInfo.HeadroomPolicyInUse = r.GetHeadRoomPolicy()
		regionInfo.ProvisionPolicyTopPriority, regionInfo.ProvisionPolicyInUse = r.GetProvisionPolicy()

		entries[regionName] = regionInfo
	}

	return entries, nil
}

// updateHeadroomForRegionEntries sets headroom for all region entries
// after headroom has been successfully updates
func (cra *cpuResourceAdvisor) updateHeadroomForRegionEntries(regionEntries types.RegionEntries) {
	for regionName := range regionEntries {
		r, ok := cra.regionMap[regionName]
		if ok {
			klog.Errorf("region %v in region entries but not in region map", regionName)
			continue
		}

		headroom, err := r.GetHeadroom()
		if err != nil {
			klog.Errorf("get headroom for region %v err: %v", regionName, err)
			continue
		}

		regionEntries[regionName].Headroom = headroom
	}
}

func (cra *cpuResourceAdvisor) assembleProvision() (InternalCalculationResult, error) {
	// generate internal calculation result.
	// must make sure pool names from cpu provision following qrm definition;
	// numa ID set as -1 means no numa-preference is needed.
	provision := InternalCalculationResult{PoolEntries: map[string]map[int]resource.Quantity{}}

	// fill in reserve pool entry
	reservePoolSize, _ := cra.metaCache.GetPoolSize(state.PoolNameReserve)
	provision.PoolEntries[state.PoolNameReserve] = map[int]resource.Quantity{
		-1: *resource.NewQuantity(int64(reservePoolSize), resource.DecimalSI),
	}

	nonNumaBindingRequirement := 0

	for _, r := range cra.regionMap {
		controlKnob, err := r.GetProvision()
		if err != nil {
			return provision, fmt.Errorf("get provision with error: %v", err)
		}

		if r.Type() == types.QoSRegionTypeShare {
			// fill in share pool entry
			sharePoolSize := int(controlKnob[types.ControlKnobNonReclaimedCPUSetSize].Value)
			provision.PoolEntries[state.PoolNameShare] = make(map[int]resource.Quantity)
			provision.PoolEntries[state.PoolNameShare][cpuadvisor.FakedNumaID] = *resource.NewQuantity(int64(sharePoolSize), resource.DecimalSI)
			nonNumaBindingRequirement += sharePoolSize

		} else if r.Type() == types.QoSRegionTypeDedicatedNumaExclusive {
			regionNumas := r.GetBindingNumas().ToSliceInt()
			if len(regionNumas) != 1 {
				klog.Errorf("region %v with type %v has invalid numa count: %v", r.Name(), r.Type(), regionNumas)
			}

			// fill in reclaim pool entry for dedicated numa exclusive regions
			reclaimPoolSize := controlKnob[types.ControlKnobReclaimedCPUSupplied].Value
			regionNuma := regionNumas[0] // Always one binding numa for this type of region
			if provision.PoolEntries[state.PoolNameReclaim] == nil {
				provision.PoolEntries[state.PoolNameReclaim] = make(map[int]resource.Quantity)
			}
			provision.PoolEntries[state.PoolNameReclaim][regionNuma] = *resource.NewQuantity(int64(reclaimPoolSize), resource.DecimalSI)
		}
	}

	// fill in reclaimed pool size of non-binding numas
	reservePoolSizeOfNonBindingNumas := int(math.Ceil(float64(reservePoolSize*cra.nonBindingNumas.Size()) / float64(cra.metaServer.NumNUMANodes)))
	reclaimPoolSizeOfNonBindingNumas := cra.nonBindingNumas.Size()*cra.metaServer.CPUsPerNuma() - nonNumaBindingRequirement - reservePoolSizeOfNonBindingNumas
	if provision.PoolEntries[state.PoolNameReclaim] == nil {
		provision.PoolEntries[state.PoolNameReclaim] = make(map[int]resource.Quantity)
	}
	provision.PoolEntries[state.PoolNameReclaim][cpuadvisor.FakedNumaID] = *resource.NewQuantity(int64(reclaimPoolSizeOfNonBindingNumas), resource.DecimalSI)
	return provision, nil
}

func (cra *cpuResourceAdvisor) gc() {
	// Delete empty regions in region map
	for regionName, r := range cra.regionMap {
		if r.IsEmpty() {
			delete(cra.regionMap, regionName)
			klog.Infof("[qosaware-cpu] delete region %v", regionName)
		}
	}
}

func (cra *cpuResourceAdvisor) getContainerRegions(ci *types.ContainerInfo) ([]region.QoSRegion, error) {
	var regions []region.QoSRegion = nil
	for regionName := range ci.RegionNames {
		r, ok := cra.regionMap[regionName]
		if !ok {
			return nil, fmt.Errorf("failed to find region %v", regionName)
		}
		regions = append(regions, r)
	}
	return regions, nil
}

func (cra *cpuResourceAdvisor) setContainerRegions(ci *types.ContainerInfo, regions []region.QoSRegion) {
	ci.RegionNames = sets.NewString()
	for _, r := range regions {
		ci.RegionNames.Insert(r.Name())
	}
}

func (cra *cpuResourceAdvisor) getPoolRegions(poolName string) ([]region.QoSRegion, error) {
	pool, ok := cra.metaCache.GetPoolInfo(poolName)
	if !ok || pool == nil {
		return nil, nil
	}

	var regions []region.QoSRegion = nil
	for regionName := range pool.RegionNames {
		r, ok := cra.regionMap[regionName]
		if !ok {
			return nil, fmt.Errorf("failed to find region %v", regionName)
		}
		regions = append(regions, r)
	}
	return regions, nil
}

func (cra *cpuResourceAdvisor) setPoolRegions(poolName string, regions []region.QoSRegion) error {
	pool, ok := cra.metaCache.GetPoolInfo(poolName)
	if !ok {
		return fmt.Errorf("failed to find pool %v", poolName)
	}

	pool.RegionNames = sets.NewString()
	for _, r := range regions {
		pool.RegionNames.Insert(r.Name())
	}
	return cra.metaCache.SetPoolInfo(poolName, pool)
}
