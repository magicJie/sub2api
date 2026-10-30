-- 前置槽位权重（fork）：落地按加权 rendezvous hash 选槽，权重越大分到的落地越多。
-- 默认 1，与原等权选择结果一致，存量落地不改派。
ALTER TABLE proxy_upstream_slots ADD COLUMN IF NOT EXISTS weight INTEGER NOT NULL DEFAULT 1;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'proxy_upstream_slots_weight_check' AND conrelid = 'proxy_upstream_slots'::regclass) THEN
        ALTER TABLE proxy_upstream_slots ADD CONSTRAINT proxy_upstream_slots_weight_check CHECK (weight BETWEEN 1 AND 100);
    END IF;
END $$;
