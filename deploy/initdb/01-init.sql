






’库同字符集/排序规则（保证外键兼容、行为一致）。
-- 注意：MYSQL_USER 默认只被授予 MYSQL_DATABASE 的权限，
-- 不在这里授权的话，测试代码连测试库建表都会被拒绝。
--
-- 两个测试库各自独立：go test ./... 会并行跑不同包的测试进程，
-- 每个测试进程都会 DROP+CREATE 自己的库，共用一个库会互相踩踏。

-- main 包的 HTTP 全链路测试
CREATE DATABASE IF NOT EXISTS agentops_test_main
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON agentops_test_main.* TO 'agentops'@'%';

-- store 包的并发/缓存测试
CREATE DATABASE IF NOT EXISTS agentops_test_store
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
GRANT ALL PRIVILEGES ON agentops_test_store.* TO 'agentops'@'%';

FLUSH PRIVILEGES;
