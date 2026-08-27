-- Migration 007: feature_cluster column on tasks (B-281)
-- Enables hierarchical decomposition: tasks grouped into feature clusters.
-- Cluster branches merge within-cluster, then cluster-to-staging.

-- ALTER TABLE tasks ADD COLUMN feature_cluster TEXT;
-- (applied programmatically in schema.go to guard against duplicate column errors)
