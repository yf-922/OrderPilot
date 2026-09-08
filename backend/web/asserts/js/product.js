document.addEventListener("DOMContentLoaded", function () {
    var editButtons = document.querySelectorAll(".js-edit-product");
    var form = document.getElementById("product-update-form");
    var createForm = document.getElementById("product-create-form");
    var createToggle = document.getElementById("show-create-product");
    var lib = window.ProductLib;

    if (!form) {
        return;
    }

    editButtons.forEach(function (button) {
        button.addEventListener("click", function () {
            lib.setValue("product-id", button.dataset.id);
            lib.setValue("product-name", button.dataset.name);
            lib.setValue("product-num", button.dataset.num);
            lib.setValue("product-image", button.dataset.image);
            lib.setValue("product-url", button.dataset.url);
            lib.setMessage("form-message", "已带入商品信息，可以直接修改后提交。", "success");
            form.hidden = false;
            if (createForm) createForm.hidden = true;
        });
    });

    form.addEventListener("submit", function (event) {
        event.preventDefault();

        var formData = new FormData(form);

        lib.requestJSON(form.action, {
            method: "POST",
            body: formData
        })
            .then(function (result) {
                if (result.success) {
                    lib.setMessage("form-message", "商品修改成功，页面即将刷新。", "success");
                    window.setTimeout(function () {
                        window.location.reload();
                    }, 600);
                    return;
                }

                lib.setMessage("form-message", result.message || "商品修改失败", "error");
            })
            .catch(function () {
                lib.setMessage("form-message", "请求失败，请检查后端接口是否正常。", "error");
            });
    });

    if (createToggle && createForm) {
        createToggle.addEventListener("click", function () {
            var creating = createForm.hidden;
            createForm.hidden = !creating;
            form.hidden = creating;
            createToggle.textContent = creating ? "返回编辑商品" : "切换到新增商品";
        });
    }

    if (createForm) {
        createForm.addEventListener("submit", function (event) {
            event.preventDefault();
            lib.requestJSON(createForm.action, {method: "POST", body: new FormData(createForm)})
                .then(function (result) {
                    if (result.success) {
                        lib.setMessage("product-create-message", "商品创建成功，页面即将刷新。", "success");
                        window.setTimeout(function () { window.location.reload(); }, 600);
                        return;
                    }
                    lib.setMessage("product-create-message", result.message || "商品创建失败", "error");
                })
                .catch(function () { lib.setMessage("product-create-message", "请求失败，请先登录后重试。", "error"); });
        });
    }

    document.querySelectorAll(".js-delete-product").forEach(function (button) {
        button.addEventListener("click", function () {
            if (!window.confirm("确认删除商品“" + button.dataset.name + "”吗？已有订单的商品不能删除。")) return;
            var body = new FormData();
            body.append("id", button.dataset.id);
            lib.requestJSON("/product/delete", {method: "POST", body: body}).then(function (result) {
                if (result.success) {
                    window.location.reload();
                    return;
                }
                lib.setMessage("form-message", result.message || "商品删除失败", "error");
            });
        });
    });
});
