-- Publication versions are local to one Instance. Publication history already
-- enforces (tenant_id, instance_id, publication_version) uniqueness in
-- xiangwan_activity_publications, so the Series-wide current-state index blocks
-- valid first publications for every later Instance in the same Series.
-- Roll-forward only: restoring the index is unsafe after valid duplicate
-- Instance-local publication versions have been committed.

DROP INDEX IF EXISTS uq_xiangwan_activity_instances_publication_version;
