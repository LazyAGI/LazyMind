-- +migrate Dialect postgres,sqlite
ALTER TABLE datasets ADD COLUMN scene VARCHAR(32) NOT NULL DEFAULT 'general';
ALTER TABLE datasets ADD COLUMN resource_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE documents ADD COLUMN resource_version BIGINT NOT NULL DEFAULT 1;
CREATE TABLE resource_operation_receipts (
 tenant_id VARCHAR(36) NOT NULL,
 user_id VARCHAR(255) NOT NULL,
 operation_key VARCHAR(255) NOT NULL,
 action VARCHAR(64) NOT NULL,
 parameter_hash VARCHAR(64) NOT NULL,
 result JSON NOT NULL,
 created_at TIMESTAMP NOT NULL,
 PRIMARY KEY (tenant_id,user_id,operation_key)
);

-- +migrate Dialect postgres
CREATE FUNCTION knowledge_document_version() RETURNS trigger AS $$
BEGIN
 IF ROW(NEW.dataset_id,NEW.p_id,NEW.display_name,NEW.document_type,NEW.tags::text,NEW.file_id,NEW.ext::text,NEW.deleted_at)
    IS DISTINCT FROM ROW(OLD.dataset_id,OLD.p_id,OLD.display_name,OLD.document_type,OLD.tags::text,OLD.file_id,OLD.ext::text,OLD.deleted_at) THEN
   NEW.resource_version := GREATEST(NEW.resource_version, OLD.resource_version + 1);
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER knowledge_document_version BEFORE UPDATE ON documents FOR EACH ROW EXECUTE FUNCTION knowledge_document_version();
CREATE FUNCTION knowledge_dataset_membership_version() RETURNS trigger AS $$
BEGIN
 IF TG_OP = 'INSERT' THEN
   UPDATE datasets SET resource_version = resource_version + 1 WHERE id = NEW.dataset_id;
 ELSIF TG_OP = 'DELETE' THEN
   UPDATE datasets SET resource_version = resource_version + 1 WHERE id = OLD.dataset_id;
 ELSIF NEW.resource_version <> OLD.resource_version OR NEW.dataset_id IS DISTINCT FROM OLD.dataset_id THEN
   UPDATE datasets SET resource_version = resource_version + 1 WHERE id = OLD.dataset_id;
   IF NEW.dataset_id IS DISTINCT FROM OLD.dataset_id THEN
     UPDATE datasets SET resource_version = resource_version + 1 WHERE id = NEW.dataset_id;
   END IF;
 END IF;
 RETURN NULL;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER knowledge_dataset_membership_version AFTER INSERT OR UPDATE OR DELETE ON documents FOR EACH ROW EXECUTE FUNCTION knowledge_dataset_membership_version();

-- +migrate Dialect sqlite
CREATE TRIGGER knowledge_document_version AFTER UPDATE ON documents
WHEN NEW.resource_version = OLD.resource_version AND (
 NEW.dataset_id IS NOT OLD.dataset_id OR NEW.p_id IS NOT OLD.p_id OR NEW.display_name IS NOT OLD.display_name OR
 NEW.document_type IS NOT OLD.document_type OR NEW.tags IS NOT OLD.tags OR NEW.file_id IS NOT OLD.file_id OR
 NEW.ext IS NOT OLD.ext OR NEW.deleted_at IS NOT OLD.deleted_at)
BEGIN
 UPDATE documents SET resource_version = OLD.resource_version + 1 WHERE id = NEW.id;
END;
CREATE TRIGGER knowledge_dataset_membership_insert AFTER INSERT ON documents
BEGIN UPDATE datasets SET resource_version = resource_version + 1 WHERE id = NEW.dataset_id; END;
CREATE TRIGGER knowledge_dataset_membership_delete AFTER DELETE ON documents
BEGIN UPDATE datasets SET resource_version = resource_version + 1 WHERE id = OLD.dataset_id; END;
CREATE TRIGGER knowledge_dataset_membership_update AFTER UPDATE ON documents
WHEN NEW.resource_version <> OLD.resource_version OR NEW.dataset_id IS NOT OLD.dataset_id
BEGIN
 UPDATE datasets SET resource_version = resource_version + 1 WHERE id = OLD.dataset_id;
 UPDATE datasets SET resource_version = resource_version + 1 WHERE id = NEW.dataset_id AND NEW.dataset_id IS NOT OLD.dataset_id AND NEW.resource_version <> OLD.resource_version;
END;
