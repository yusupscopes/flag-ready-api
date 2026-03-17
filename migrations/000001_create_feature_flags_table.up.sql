CREATE TABLE IF NOT EXISTS feature_flags (
    name VARCHAR(255) PRIMARY KEY,
    enabled BOOLEAN NOT NULL,
    rollout_percentage INT DEFAULT 100
);

-- Seed data
INSERT INTO feature_flags (name, enabled, rollout_percentage) VALUES ('new_dashboard', true, 50) ON CONFLICT DO NOTHING;
INSERT INTO feature_flags (name, enabled, rollout_percentage) VALUES ('beta_checkout', false, 0) ON CONFLICT DO NOTHING;