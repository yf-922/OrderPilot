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
                        lib.setToken(result.data && result.data.token);
                        lib.setMessage("user-login-message", "登录校验通过，用户：" + result.data.userName, "success");
                        loadProfile();
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
                        lib.setMessage("user-create-message", "用户创建成功：" + result.data.userName, "success");
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

    var profileForm = document.getElementById("user-profile-form");
    function loadProfile() {
        var token = lib.getToken();
        if (!token) return;
        var payload = token.split(".")[0];
        // 用户 ID 在签名 token 的 payload 中，实际资料仍从后端读取。
        try {
            payload += "=".repeat((4 - payload.length % 4) % 4);
            var decoded = atob(payload.replace(/-/g, "+").replace(/_/g, "/"));
            var id = decoded.split(".")[0];
            lib.requestJSON("/user/" + id).then(function (result) {
                if (!result.success || !result.data) return;
                lib.setValue("profile-id", result.data.id);
                lib.setValue("profile-nick-name", result.data.nickName);
                lib.setValue("profile-user-name", result.data.userName);
                lib.setMessage("session-message", "已登录用户：" + result.data.userName, "success");
            });
        } catch (_) {}
    }

    if (profileForm) {
        profileForm.addEventListener("submit", function (event) {
            event.preventDefault();
            lib.requestJSON(profileForm.action, {method: "POST", body: new FormData(profileForm)})
                .then(function (result) {
                    lib.setMessage("user-profile-message", result.message || (result.success ? "保存成功" : "保存失败"), result.success ? "success" : "error");
                    if (result.success) loadProfile();
                });
        });
    }

    if (lib.getToken()) loadProfile();
});
