-- resource & journal creates
drop table if exists "Journal";
drop table if exists "Resources";
drop index if exists "IX_Resources_OwnerId";

CREATE TABLE IF NOT EXISTS "Journal" (
                                         "Clock" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY,
                                         "Resource" text NULL,
                                         "CreatedAt" timestamp without time zone NOT NULL,
                                         "PartitionName" varchar(20) NULL,
                                         CONSTRAINT "PK_Journal" PRIMARY KEY ("Clock", "PartitionName");
);

CREATE TABLE IF NOT EXISTS "Resources" (
                                           "Id" varchar(50) NOT NULL,
                                           "OwnerId" varchar(50) NOT NULL,
                                           "Version" integer NOT NULL,
                                           "CreatedAt" timestamp without time zone NOT NULL,
                                           "UpdatedAt" timestamp without time zone NOT NULL,
                                           "Deleted" boolean NOT NULL,
                                           "Resource" text NULL,
                                           CONSTRAINT "PK_Resources" PRIMARY KEY ("Id");
);

CREATE INDEX IF NOT EXISTS "IX_Resources_OwnerId" ON "Resources" ("OwnerId");

--select * from public."Journal";
--select * from public."Resources";

SELECT MAX("Clock") AS "Clock" FROM public."Journal";