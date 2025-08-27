CREATE DATABASE nasa;
USE nasa;

CREATE TABLE apod (  
    apod_date        DATE NOT NULL PRIMARY KEY,
    title            VARCHAR(255) NOT NULL,
    explanation      TEXT NOT NULL,
    media_type       VARCHAR(10) NOT NULL,
    url              VARCHAR(1024) NOT NULL,
    hdurl            VARCHAR(1024),
    service_version  VARCHAR(10),
    copyright        VARCHAR(255),
    thumbnail_url    VARCHAR(1024)
);
