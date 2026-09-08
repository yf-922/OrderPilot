document.addEventListener("DOMContentLoaded", function () {
    var lib = window.ProductLib;
    var loginForm = document.getElementById("user-login-form");
    var createForm = document.getElementById("user-create-form");

    if (!lib || (!loginForm && !createForm)) {
        return;
    }

    if (loginForm) {
        loginForm.addEventListener("submit", function (event) {
            event.preventDefault();

            lib.requestJSON(loginForm.action, {
                method: "POST",
                body: new FormData(loginForm)
            })
                .then(function (result) {
                    if (result.success) {
                        lib.setMessage("user-login-message", "登录校验通过，用户：" + result.user.userName, "success");
                        return;
                    }

                    lib.setMessage("user-login-message", result.message || "登录失败", "error");
                })
                .catch(function () {
                    lib.setMessage("user-login-message", "登录请求失败", "error");
                });
        });
    }

    if (createForm) {
        createForm.addEventListener("submit", function (event) {
            event.preventDefault();

            lib.requestJSON(createForm.action, {
                method: "POST",
                body: new FormData(createForm)
            })
                .then(function (result) {
                    if (result.success) {
                        lib.setMessage("user-create-message", "用户创建成功：" + result.user.userName, "success");
                        createForm.reset();
                        return;
                    }

                    lib.setMessage("user-create-message", result.message || "创建失败", "error");
                })
                .catch(function () {
                    lib.setMessage("user-create-message", "创建请求失败", "error");
                });
        });
    }
});
