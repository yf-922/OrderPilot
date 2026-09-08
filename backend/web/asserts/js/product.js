document.addEventListener("DOMContentLoaded", function () {
    var editButtons = document.querySelectorAll(".js-edit-product");
    var form = document.getElementById("product-update-form");
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
});
