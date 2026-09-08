USE `product`;

INSERT INTO `User` (`NickName`, `Username`, `HashPassword`)
VALUES ('Demo User', 'demo', SHA2('demo123456', 256))
ON DUPLICATE KEY UPDATE `Username` = VALUES(`Username`);

INSERT INTO `product` (`productName`, `productNum`, `productImage`, `productUrl`)
SELECT 'Go 后端工程实践', 100, 'https://dummyimage.com/640x360', 'https://go.dev/'
WHERE NOT EXISTS (SELECT 1 FROM `product` WHERE `productName` = 'Go 后端工程实践');
