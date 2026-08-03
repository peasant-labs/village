-- Backfill titles for existing transcripts that have no title.
-- Priority: 1) title_generated, 2) derive from project + model info.

-- Step 1: Use title_generated where available
UPDATE transcripts
SET title = title_generated
WHERE title IS NULL
  AND title_generated IS NOT NULL
  AND title_generated != '';

-- Step 2: Derive from project_name + model_name for remaining NULL titles.
-- Project name extraction: find "GitHub-" in dash-delimited path and take
-- everything after it (preserving multi-word project names like
-- "data-leverage-village"). Falls back to last slash segment.
UPDATE transcripts
SET title = CASE
  WHEN project_name IS NOT NULL AND model_name IS NOT NULL THEN
    CASE
      WHEN project_name ~ '-GitHub-' THEN
        SUBSTRING(project_name FROM '-GitHub-(.+)$')
      WHEN project_name ~ '/' THEN
        SUBSTRING(project_name FROM '[^/]+$')
      ELSE project_name
    END
    || ' - ' ||
    INITCAP(REPLACE(
      REGEXP_REPLACE(
        REGEXP_REPLACE(model_name, '-\d{8,}$', ''),
        '(\d)-(\d)', '\1.\2', 'g'),
      '-', ' '
    )) || ' session'
  WHEN project_name IS NOT NULL THEN
    CASE
      WHEN project_name ~ '-GitHub-' THEN
        SUBSTRING(project_name FROM '-GitHub-(.+)$')
      WHEN project_name ~ '/' THEN
        SUBSTRING(project_name FROM '[^/]+$')
      ELSE project_name
    END
    || ' session'
  WHEN model_name IS NOT NULL THEN
    INITCAP(REPLACE(
      REGEXP_REPLACE(
        REGEXP_REPLACE(model_name, '-\d{8,}$', ''),
        '(\d)-(\d)', '\1.\2', 'g'),
      '-', ' '
    )) || ' session'
  ELSE NULL
END
WHERE title IS NULL;
