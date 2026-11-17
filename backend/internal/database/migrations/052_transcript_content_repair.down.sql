DROP TRIGGER trg_repair_audit_delete_guard ON transcript_content_repairs;
DROP FUNCTION repair_audit_block_delete();
DROP TRIGGER trg_repair_audit_immutable ON transcript_content_repairs;
DROP FUNCTION repair_audit_block_update();
DROP TRIGGER trg_repair_audit_actor ON transcript_content_repairs;
DROP FUNCTION repair_audit_assign_actor();
DROP TABLE transcript_content_repairs;
