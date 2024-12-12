CREATE TABLE IF NOT EXISTS user_projector_checkpoint (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE,
    commit_position BIGINT NOT NULL,
    prepare_position BIGINT NOT NULL,
    CONSTRAINT single_row CHECK (id)
); 