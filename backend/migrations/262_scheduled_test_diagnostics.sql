-- Extend existing scheduled tests with model fingerprint and quality diagnostics.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS test_type VARCHAR(20) NOT NULL DEFAULT 'connectivity'
        CHECK (test_type IN ('connectivity', 'fingerprint', 'quality', 'comprehensive')),
    ADD COLUMN IF NOT EXISTS expected_model VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS quality_threshold INT NOT NULL DEFAULT 80
        CHECK (quality_threshold BETWEEN 1 AND 100);

ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS diagnostics JSONB;
