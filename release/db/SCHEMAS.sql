CREATE TABLE orders ( 
 id serial PRIMARY KEY,
 origin VARCHAR (50)  NOT NULL,
 destination VARCHAR (50) NOT NULL,
 distance VARCHAR (50) NOT NULL,
 status VARCHAR (50) NOT NULL,
 created_at TIMESTAMP DEFAULT NOW()
);



