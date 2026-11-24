-- +goose Up
-- Ánh xạ dẫn xuất của ConceptMap (D4): khóa theo id ConceptMap, không nằm trong JSON của resource.
CREATE TABLE fhir_concept_mapping (
    map_id         text NOT NULL,
    source_system  text NOT NULL,
    source_code    text NOT NULL,
    source_display text NOT NULL DEFAULT '',
    target_system  text NOT NULL,
    target_code    text NOT NULL,
    target_display text NOT NULL DEFAULT '',
    equivalence    text NOT NULL DEFAULT '',
    comment        text NOT NULL DEFAULT '',
    attributes     text NOT NULL DEFAULT '',
    PRIMARY KEY (map_id, source_system, source_code, target_system, target_code)
);
CREATE INDEX fhir_concept_mapping_target ON fhir_concept_mapping (map_id, target_system, target_code);

-- +goose Down
DROP TABLE fhir_concept_mapping;
