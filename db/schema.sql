CREATE DATABASE IF NOT EXISTS `product` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE `product`;

CREATE TABLE IF NOT EXISTS `User` (
  `ID` BIGINT PRIMARY KEY AUTO_INCREMENT,
  `NickName` VARCHAR(64) NOT NULL,
  `Username` VARCHAR(64) NOT NULL UNIQUE,
  `HashPassword` VARCHAR(255) NOT NULL,
  `createdAt` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `product` (
  `ID` BIGINT PRIMARY KEY AUTO_INCREMENT,
  `productName` VARCHAR(160) NOT NULL,
  `productNum` BIGINT NOT NULL DEFAULT 0,
  `productImage` VARCHAR(512) NOT NULL,
  `productUrl` VARCHAR(512) NOT NULL,
  `createdAt` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updatedAt` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT `chk_product_stock` CHECK (`productNum` >= 0),
  INDEX `idx_product_name` (`productName`)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS `order` (
  `ID` BIGINT PRIMARY KEY AUTO_INCREMENT,
  `userID` BIGINT NOT NULL,
  `productID` BIGINT NOT NULL,
  `quantity` BIGINT NOT NULL DEFAULT 1,
  `idempotencyKey` VARCHAR(128) NOT NULL,
  `orderStatus` TINYINT NOT NULL DEFAULT 0,
  `createdAt` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updatedAt` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  CONSTRAINT `chk_order_quantity` CHECK (`quantity` > 0),
  CONSTRAINT `chk_order_status` CHECK (`orderStatus` IN (0, 1, 2)),
  CONSTRAINT `fk_order_user` FOREIGN KEY (`userID`) REFERENCES `User` (`ID`),
  CONSTRAINT `fk_order_product` FOREIGN KEY (`productID`) REFERENCES `product` (`ID`),
  INDEX `idx_order_user_created` (`userID`, `createdAt`),
  INDEX `idx_order_status_created` (`orderStatus`, `createdAt`),
  UNIQUE INDEX `uk_order_idempotency` (`idempotencyKey`)
) ENGINE=InnoDB;
