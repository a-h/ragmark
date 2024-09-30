-- Document stores the metadata of the documents that are indexed.
-- The path is the URI of the document, e.g. /suppliers/1234
create table document(
    path text not null primary key,
    last_updated timestamp not null
);

-- Create full-text search table for documents.
create virtual table document_fts using fts5(
    path unindexed,
    title,
    text,
    summary unindexed
);

-- A chunk is a part of a document that is processed separately, typically a sentence or paragaph.
-- The path is the URI of the document that the chunk belongs to, e.g. /suppliers/1234
-- The idx column is used to order the chunks in the document.
create table chunk(
    path text not null,
    idx integer not null,
    text text not null
);

-- Create embedding table for storing vector embeddings of document chunks.
create virtual table chunk_embedding using vec0(
    embedding float[768]
);

-- Create triple store for knowledge graph, using a JSON column to store the triple,
-- but exporting the subject, predicate, and object as separate columns for indexing.
create table triple(
    triple text not null,
    subject text generated always as (json_extract(triple, '$.s')) virtual not null,
    predicate text generated always as (json_extract(triple, '$.p')) virtual not null,
    object text generated always as (json_extract(triple, '$.o')) virtual not null
);

-- Create indexes for triple store so that we can query it efficiently
-- by the subject, predicate, or object.
create index triple_subject on triple(subject);
create index triple_predicate on triple(predicate);
create index triple_object on triple(object);

-- The triples should be unique, so we create a unique index on the three columns.
create unique index triple_unique on triple(subject, predicate, object);
