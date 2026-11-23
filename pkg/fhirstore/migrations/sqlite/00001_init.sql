-- +goose Up
CREATE TABLE fhir_resource (
    resource_type text NOT NULL,
    id            text NOT NULL,
    version_id    text NOT NULL,
    last_updated  text NOT NULL,
    deleted       boolean NOT NULL DEFAULT false,
    content       text NOT NULL,
    PRIMARY KEY (resource_type, id)
);

CREATE TABLE fhir_history (
    seq           INTEGER PRIMARY KEY AUTOINCREMENT,
    resource_type text NOT NULL,
    id            text NOT NULL,
    version_id    text NOT NULL,
    last_updated  text NOT NULL,
    method        text NOT NULL,
    deleted       boolean NOT NULL,
    content       text NOT NULL,
    UNIQUE (resource_type, id, version_id)
);
CREATE INDEX fhir_history_by_resource ON fhir_history (resource_type, id, seq);

CREATE TABLE fhir_idx_string (
    resource_type text NOT NULL,
    id            text NOT NULL,
    code          text NOT NULL,
    ord           integer NOT NULL,
    raw text NOT NULL, norm text NOT NULL
);
CREATE INDEX fhir_idx_string_owner ON fhir_idx_string (resource_type, id);
CREATE INDEX fhir_idx_string_search ON fhir_idx_string (resource_type, code, norm);

CREATE TABLE fhir_idx_token (
    resource_type text NOT NULL,
    id            text NOT NULL,
    code          text NOT NULL,
    ord           integer NOT NULL,
    kind char(1) NOT NULL, system text, value text NOT NULL
);
CREATE INDEX fhir_idx_token_owner ON fhir_idx_token (resource_type, id);
CREATE INDEX fhir_idx_token_search ON fhir_idx_token (resource_type, code, value);

CREATE TABLE fhir_idx_reference (
    resource_type text NOT NULL,
    id            text NOT NULL,
    code          text NOT NULL,
    ord           integer NOT NULL,
    raw text NOT NULL, target text NOT NULL, ref_type text NOT NULL, ref_id text NOT NULL
);
CREATE INDEX fhir_idx_reference_owner ON fhir_idx_reference (resource_type, id);
CREATE INDEX fhir_idx_reference_search ON fhir_idx_reference (resource_type, code, target);

CREATE TABLE fhir_idx_date (
    resource_type text NOT NULL,
    id            text NOT NULL,
    code          text NOT NULL,
    ord           integer NOT NULL,
    lo bigint NOT NULL, hi bigint NOT NULL
);
CREATE INDEX fhir_idx_date_owner ON fhir_idx_date (resource_type, id);
CREATE INDEX fhir_idx_date_search ON fhir_idx_date (resource_type, code, lo);

CREATE TABLE fhir_idx_number (
    resource_type text NOT NULL,
    id            text NOT NULL,
    code          text NOT NULL,
    ord           integer NOT NULL,
    value double precision NOT NULL
);
CREATE INDEX fhir_idx_number_owner ON fhir_idx_number (resource_type, id);
CREATE INDEX fhir_idx_number_search ON fhir_idx_number (resource_type, code, value);

CREATE TABLE fhir_idx_quantity (
    resource_type text NOT NULL,
    id            text NOT NULL,
    code          text NOT NULL,
    ord           integer NOT NULL,
    value double precision NOT NULL, system text NOT NULL, qcode text NOT NULL, unit text NOT NULL
);
CREATE INDEX fhir_idx_quantity_owner ON fhir_idx_quantity (resource_type, id);
CREATE INDEX fhir_idx_quantity_search ON fhir_idx_quantity (resource_type, code, value);

CREATE TABLE fhir_idx_uri (
    resource_type text NOT NULL,
    id            text NOT NULL,
    code          text NOT NULL,
    ord           integer NOT NULL,
    value text NOT NULL, norm text NOT NULL
);
CREATE INDEX fhir_idx_uri_owner ON fhir_idx_uri (resource_type, id);
CREATE INDEX fhir_idx_uri_search ON fhir_idx_uri (resource_type, code, value);

-- +goose Down
DROP TABLE fhir_idx_string;
DROP TABLE fhir_idx_token;
DROP TABLE fhir_idx_reference;
DROP TABLE fhir_idx_date;
DROP TABLE fhir_idx_number;
DROP TABLE fhir_idx_quantity;
DROP TABLE fhir_idx_uri;
DROP TABLE fhir_history;
DROP TABLE fhir_resource;
