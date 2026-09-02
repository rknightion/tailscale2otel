package collector

// defaultCheckpointShard is the temporary single-shard identity used while the
// Kubernetes checkpoint store still writes one shared map. TSO-0110 replaces
// ShardKey's body with the collector-namespace mapping while keeping this seam
// shared by storage and configuration projection.
const defaultCheckpointShard = "default"

// ShardKey returns the checkpoint shard identity for a fully constructed
// checkpoint key. The first sharded-store increment deliberately maps every
// key to one shard so the projection and the existing backend agree exactly;
// the function is the seam TSO-0110 will replace when storage is sharded.
func ShardKey(_ string) string { return defaultCheckpointShard }
