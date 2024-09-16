create table documents(
    path text not null primary key,
    last_updated timestamp not null
);
create virtual table documents_fts using fts5(
    document_id unindexed,
    title,
    text,
    summary unindexed
);
create virtual table documents_embeddings using vec0(
    embedding float[5120]
);
