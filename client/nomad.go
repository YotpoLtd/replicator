package client

import (
	"fmt"
	"math"
	"time"

	"github.com/dariubs/percent"
	"github.com/elsevier-core-engineering/replicator/helper"
	"github.com/elsevier-core-engineering/replicator/logging"
	"github.com/elsevier-core-engineering/replicator/replicator/structs"
	nomad "github.com/hashicorp/nomad/api"
)

// Scaling metric types indicate the most-utilized resource across the cluster. When evaluating
// scaling decisions, the most-utilized resource will be prioritized.
const (
	ScalingMetricNone      = "None" // All supported allocation resources are unutilized.
	ScalingMetricDisk      = "Disk"
	ScalingMetricMemory    = "Memory"
	ScalingMetricProcessor = "CPU"
)

// Scaling direction types indicate the allowed scaling actions.
const (
	ScalingDirectionOut  = "Out"
	ScalingDirectionIn   = "In"
	ScalingDirectionNone = "None"
)

// Set of possible states for a job.
const (
	StatePending = "pending"
	StateRunning = "running"
	StateDead    = "dead"
)

const scaleInCapacityThreshold = 90.0
const bytesPerMegabyte = 1024000

// Provides a wrapper to the Nomad API package.
type nomadClient struct {
	nomad *nomad.Client
}

// NewNomadClient is used to create a new client to interact with Nomad. The
// client implements the NomadClient interface.
func NewNomadClient(addr string) (structs.NomadClient, error) {
	config := nomad.DefaultConfig()
	config.Address = addr
	c, err := nomad.NewClient(config)
	if err != nil {
		return nil, err
	}

	return &nomadClient{nomad: c}, nil
}

// NodeReverseLookup provides a method to get the ID of the worker pool node
// running a given allocation.
func (c *nomadClient) NodeReverseLookup(allocID string) (node string, err error) {
	resp, _, err := c.nomad.Allocations().Info(allocID, &nomad.QueryOptions{})
	if err != nil {
		return
	}

	if len(resp.NodeID) == 0 {
		err = fmt.Errorf("The node reverse lookup returned an empty result")
		return
	}

	return resp.NodeID, nil
}

// MostUtilizedResource calculates the resource that is most-utilized across the cluster.
// This is used to determine the resource that should be prioritized when making scaling
// decisions like determining the least-allocated worker node.
//
// If all resources are completely unutilized, the scaling metric will be set to `None`
// and the daemon will take no actions.
func (c *nomadClient) MostUtilizedResource(alloc *structs.ClusterCapacity) {
	// Determine the resource that is consuming the greatest percentage of its
	// overall worker pool capacity.
	max := (helper.Max(alloc.UsedCapacity.CPUPercent,
		alloc.UsedCapacity.MemoryPercent, alloc.UsedCapacity.DiskPercent))

	// Set the compute cluster scaling metric to the most-utilized resource.
	switch max {
	case 0:
		alloc.ScalingMetric = ScalingMetricNone
	case alloc.UsedCapacity.CPUPercent:
		alloc.ScalingMetric = ScalingMetricProcessor
	case alloc.UsedCapacity.DiskPercent:
		alloc.ScalingMetric = ScalingMetricDisk
	case alloc.UsedCapacity.MemoryPercent:
		alloc.ScalingMetric = ScalingMetricMemory
	}
}

// MostUtilizedGroupResource determines whether CPU or Mem are the most utilized
// resource of a Group.
func (c *nomadClient) MostUtilizedGroupResource(gsp *structs.GroupScalingPolicy) {
	max := (helper.Max(gsp.Tasks.Resources.CPUPercent,
		gsp.Tasks.Resources.MemoryPercent))

	switch max {
	case gsp.Tasks.Resources.CPUPercent:
		gsp.ScalingMetric = ScalingMetricProcessor
	case gsp.Tasks.Resources.MemoryPercent:
		gsp.ScalingMetric = ScalingMetricMemory
	}
}

// LeastAllocatedNode determines which worker pool node is consuming the least
// amount of the cluster's most-utilized resource. If Replicator is running as
// a Nomad job, the worker node running the Replicator leader will be excluded.
func (c *nomadClient) LeastAllocatedNode(capacity *structs.ClusterCapacity,
	state *structs.ScalingState) (nodeID, nodeIP string) {
	var lowest float64

	for _, alloc := range capacity.NodeAllocations {
		// If we've encountered a protected worker pool node, exclude it from
		// least-allocated node discovery.
		if alloc.NodeID == state.ProtectedNode {
			logging.Debug("client/nomad: Node %v will be excluded when calculating "+
				"eligible worker pool nodes to be removed", state.ProtectedNode)
			continue
		}

		switch capacity.ScalingMetric {
		case ScalingMetricProcessor:
			if (lowest == 0) || (alloc.UsedCapacity.CPUPercent < lowest) {
				nodeID = alloc.NodeID
				lowest = alloc.UsedCapacity.CPUPercent
			}
		case ScalingMetricMemory:
			if (lowest == 0) || (alloc.UsedCapacity.MemoryPercent < lowest) {
				nodeID = alloc.NodeID
				lowest = alloc.UsedCapacity.MemoryPercent
			}
		case ScalingMetricNone:
			nodeID = alloc.NodeID
		}
	}

	// In order to perform downscaling of the cluster we need to have access
	// to the nodes IP address so  the AWS instance-id can be inferred.
	resp, _, err := c.nomad.Nodes().Info(nodeID, &nomad.QueryOptions{})
	if err != nil {
		logging.Error("client/nomad: unable to determine nomad node IP address: %v", err)
	}
	nodeIP = resp.Attributes["unique.network.ip-address"]

	return
}

// DrainNode toggles the drain mode of a worker node. When enabled, no further allocations
// will be assigned and existing allocations will be migrated.
func (c *nomadClient) DrainNode(nodeID string) (err error) {
	// Initiate allocation draining for specified node.
	_, err = c.nomad.Nodes().ToggleDrain(nodeID, true, &nomad.WriteOptions{})
	if err != nil {
		return err
	}

	// Validate node has been placed in drain mode; fail fast if the node
	// failed to enter drain mode.
	resp, _, err := c.nomad.Nodes().Info(nodeID, &nomad.QueryOptions{})
	if (err != nil) || (resp.Drain != true) {
		return err
	}
	logging.Info("client/nomad: node %v has been placed in drain mode\n", nodeID)

	// Setup a ticker to poll the node allocations and report when all existing
	// allocations have been migrated to other worker nodes.
	ticker := time.NewTicker(time.Millisecond * 500)
	timeout := time.Tick(time.Minute * 3)

	for {
		select {
		case <-timeout:
			logging.Error("client/nomad: timeout %v reached while waiting for existing allocations to be migrated from node %v",
				timeout, nodeID)
			return nil
		case <-ticker.C:
			activeAllocations := 0

			// Get allocations assigned to the specified node.
			allocations, _, err := c.nomad.Nodes().Allocations(nodeID, &nomad.QueryOptions{})
			if err != nil {
				return err
			}

			// Iterate over allocations, if any are running or pending, increment the active
			// allocations counter.
			for _, nodeAlloc := range allocations {
				if (nodeAlloc.ClientStatus == StateRunning) || (nodeAlloc.ClientStatus == "pending") {
					activeAllocations++
				}
			}

			if activeAllocations == 0 {
				logging.Info("client/nomad: node %v has no active allocations", nodeID)
				return nil
			}

			logging.Info("client/nomad: node %v has %v active allocations, pausing and will re-poll allocations", nodeID, activeAllocations)
		}
	}
}

// GetTaskGroupResources finds the defined resource requirements for a
// given Job.
func (c *nomadClient) GetTaskGroupResources(jobName string, groupPolicy *structs.GroupScalingPolicy) error {
	jobs, _, err := c.nomad.Jobs().Info(jobName, &nomad.QueryOptions{})

	if err != nil {
		return err
	}

	for _, group := range jobs.TaskGroups {
		for _, task := range group.Tasks {
			groupPolicy.Tasks.Resources.CPUMHz += *task.Resources.CPU
			groupPolicy.Tasks.Resources.MemoryMB += *task.Resources.MemoryMB
		}
	}
	return nil
}

// EvaluateJobScaling identifies Nomad allocations representative of a Job group
// and compares the consumed resource percentages against the scaling policy to
// determine whether a scaling event is required.
func (c *nomadClient) EvaluateJobScaling(jobName string, jobScalingPolicies []*structs.GroupScalingPolicy) (err error) {
	for _, gsp := range jobScalingPolicies {
		if err = c.GetTaskGroupResources(jobName, gsp); err != nil {
			return
		}

		allocs, _, err := c.nomad.Jobs().Allocations(jobName, false, &nomad.QueryOptions{})
		if err != nil {
			return err
		}

		c.GetJobAllocations(allocs, gsp)
		c.MostUtilizedGroupResource(gsp)

		switch gsp.ScalingMetric {
		case ScalingMetricProcessor:
			if gsp.Tasks.Resources.CPUPercent > gsp.ScaleOutCPU {
				gsp.ScaleDirection = ScalingDirectionOut
			}
		case ScalingMetricMemory:
			if gsp.Tasks.Resources.MemoryPercent > gsp.ScaleOutMem {
				gsp.ScaleDirection = ScalingDirectionOut
			}
		}

		if (gsp.Tasks.Resources.CPUPercent < gsp.ScaleInCPU) &&
			(gsp.Tasks.Resources.MemoryPercent < gsp.ScaleInMem) {
			gsp.ScaleDirection = ScalingDirectionIn
		}
	}
	return
}

// GetJobAllocations identifies all allocations for an active job.
func (c *nomadClient) GetJobAllocations(allocs []*nomad.AllocationListStub, gsp *structs.GroupScalingPolicy) {
	for _, allocationStub := range allocs {
		if (allocationStub.ClientStatus == StateRunning) && (allocationStub.DesiredStatus == "run") {
			if alloc, _, err := c.nomad.Allocations().Info(allocationStub.ID, &nomad.QueryOptions{}); err == nil && alloc != nil {
				c.GetAllocationStats(alloc, gsp)
			}
		}
	}
}

// VerifyNodeHealth evaluates whether a specified worker node is a healthy
// member of the Nomad cluster.
func (c *nomadClient) VerifyNodeHealth(nodeIP string) (healthy bool) {
	// Setup a ticker to poll the health status of the specified worker node
	// and retry up to a specified timeout.
	ticker := time.NewTicker(time.Second * 10)
	timeout := time.Tick(time.Minute * 5)

	logging.Debug("client/nomad: verifying health of node %v", nodeIP)

	for {
		select {
		case <-timeout:
			logging.Error("client/nomad: timeout reached while verifying the "+
				"health of worker node %v", nodeIP)
			return
		case <-ticker.C:
			// Retrieve a list of all worker nodes within the cluster.
			nodes, _, err := c.nomad.Nodes().List(&nomad.QueryOptions{})
			if err != nil {
				return
			}

			// Iterate over nodes and evaluate health status.
			for _, node := range nodes {
				// Skip the node if it is not in a healthy state.
				if node.Status != "ready" {
					continue
				}

				// Retrieve detailed information about the worker node.
				resp, _, err := c.nomad.Nodes().Info(node.ID, &nomad.QueryOptions{})
				if err != nil {
					logging.Error("client/nomad: an error occurred while attempting to "+
						"retrieve details about node %v: %v", node.ID, err)
				}

				// If the healthy worker node matches the specified worker node,
				// set the response to healthy and exit.
				if resp.Attributes["unique.network.ip-address"] == nodeIP {
					logging.Info("client/nomad: successfully verified the health of "+
						"node %v", nodeIP)
					healthy = true
					return
				}
			}

			logging.Debug("client/nomad: unable to verify the health of node %v, "+
				"pausing and will re-evaluate node health.", nodeIP)
		}
	}
}

// GetAllocationStats discovers the resources consumed by a particular Nomad
// allocation.
func (c *nomadClient) GetAllocationStats(allocation *nomad.Allocation, scalingPolicy *structs.GroupScalingPolicy) {
	stats, err := c.nomad.Allocations().Stats(allocation, &nomad.QueryOptions{})
	if err != nil {
		logging.Error("client/nomad: failed to retrieve allocation statistics from client %v: %v\n", allocation.NodeID, err)
		return
	}

	cs := stats.ResourceUsage.CpuStats
	ms := stats.ResourceUsage.MemoryStats

	scalingPolicy.Tasks.Resources.CPUPercent = percent.PercentOf(int(math.Floor(cs.TotalTicks)),
		scalingPolicy.Tasks.Resources.CPUMHz)
	scalingPolicy.Tasks.Resources.MemoryPercent = percent.PercentOf(int((ms.RSS / bytesPerMegabyte)),
		scalingPolicy.Tasks.Resources.MemoryMB)
}

// MaxAllowedClusterUtilization calculates the maximum allowed cluster utilization after
// taking into consideration node fault-tolerance and scaling overhead.
func MaxAllowedClusterUtilization(capacity *structs.ClusterCapacity, nodeFaultTolerance int, scaleIn bool) (maxAllowedUtilization int) {
	var allocTotal, capacityTotal int
	var internalScalingMetric string

	// Use the cluster scaling metric when determining total cluster capacity
	// and task group scaling overhead.
	switch capacity.ScalingMetric {
	case ScalingMetricMemory:
		internalScalingMetric = ScalingMetricMemory
		allocTotal = capacity.TaskAllocation.MemoryMB
		capacityTotal = capacity.TotalCapacity.MemoryMB
	default:
		internalScalingMetric = ScalingMetricProcessor
		allocTotal = capacity.TaskAllocation.CPUMHz
		capacityTotal = capacity.TotalCapacity.CPUMHz
	}

	nodeAvgAlloc := capacityTotal / capacity.NodeCount
	if scaleIn {
		capacityTotal = capacityTotal - nodeAvgAlloc
	}

	logging.Debug("client/nomad: Cluster Capacity (CPU [MHz]: %v, Memory [MB]: %v)",
		capacity.TotalCapacity.CPUMHz, capacity.TotalCapacity.MemoryMB)
	logging.Debug("client/nomad: Cluster Utilization (Scaling Metric: %v, CPU [MHz]: %v, Memory [MB]: %v)",
		capacity.ScalingMetric, capacity.UsedCapacity.CPUMHz, capacity.UsedCapacity.MemoryMB)
	logging.Debug("client/nomad: Scaling Metric (Algorithm): %v, Average Node Capacity: %v, Job Scaling Overhead: %v",
		internalScalingMetric, nodeAvgAlloc, allocTotal)

	maxAllowedUtilization = ((capacityTotal - allocTotal) - (nodeAvgAlloc * nodeFaultTolerance))

	return
}
