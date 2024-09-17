create table document(
    path text not null primary key,
    last_updated timestamp not null
);
create virtual table document_fts using fts5(
    path unindexed,
    title,
    text,
    summary unindexed
);
create table chunk(
    path text not null,
    idx integer not null,
    text text not null
);
create virtual table chunk_embedding using vec0(
    embedding float[5120]
);
