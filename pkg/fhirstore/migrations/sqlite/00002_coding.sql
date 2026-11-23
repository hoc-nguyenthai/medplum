-- +goose Up
-- Concept dẫn xuất của CodeSystem (Q-D1 = A): khóa theo id CodeSystem, không nằm trong JSON của resource.
CREATE TABLE fhir_coding (
    system_id text NOT NULL,
    code      text NOT NULL,
    display   text NOT NULL DEFAULT '',
    PRIMARY KEY (system_id, code)
);

CREATE TABLE fhir_coding_property (
    system_id text NOT NULL,
    code      text NOT NULL,
    property  text NOT NULL,
    type      text NOT NULL,
    value     text NOT NULL,
    PRIMARY KEY (system_id, code, property, value)
);
CREATE INDEX fhir_coding_property_target ON fhir_coding_property (system_id, property, value);

CREATE TABLE fhir_coding_designation (
    system_id text NOT NULL,
    code      text NOT NULL,
    language  text NOT NULL DEFAULT '',
    value     text NOT NULL,
    PRIMARY KEY (system_id, code, language, value)
);

-- +goose Down
DROP TABLE fhir_coding_designation;
DROP TABLE fhir_coding_property;
DROP TABLE fhir_coding;
