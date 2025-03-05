CREATE TABLE IF NOT EXISTS projector_checkpoint (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) UNIQUE NOT NULL,
    commit_position BIGINT NOT NULL,
    prepare_position BIGINT NOT NULL
);