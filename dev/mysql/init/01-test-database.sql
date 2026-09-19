-- A separate database for `go test`: the MySQL store tests delete every row
-- before each test, so they must never point at graph_ops.
CREATE DATABASE IF NOT EXISTS graph_ops_test CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
GRANT ALL PRIVILEGES ON graph_ops_test.* TO 'graphops'@'%';
FLUSH PRIVILEGES;
