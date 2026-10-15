-- +goose Up
-- Class enrolment tokens (#63, docs/adr/0035-class-enrolment-tokens.md): a token with
-- class = true is not consumed. Its binding column holds a prefix ("agent"), and each use
-- mints the binding one segment below it that the client asks for ("agent:node-1"), as long
-- as no unexpired, unrevoked certificate holds that binding already. A DaemonSet shares one
-- such token instead of needing one token per node.

ALTER TABLE enrol_tokens ADD COLUMN class boolean NOT NULL DEFAULT false;

-- +goose Down
-- Without the column a class token would read as a one-time token for its prefix: it goes.
DELETE FROM enrol_tokens WHERE class;
ALTER TABLE enrol_tokens DROP COLUMN class;
