type ReasonDialogOptions = { title: string; description?: string; initialValue?: string };

// Native modal semantics provide focus trapping, Escape cancellation, and a
// labelled form. No write is dispatched until the caller receives a reason.
export function requestRequiredReason(options: ReasonDialogOptions): Promise<string | null> {
  return new Promise((resolve) => {
    const previousFocus = document.activeElement;
    const dialog = document.createElement("dialog");
    dialog.className = "required-reason-dialog";
    const id = `reason-${crypto.randomUUID()}`;
    const form = document.createElement("form");
    const heading = document.createElement("h2");
    heading.id = `${id}-title`;
    heading.textContent = options.title;
    dialog.setAttribute("aria-labelledby", heading.id);
    form.append(heading);
    if (options.description) {
      const description = document.createElement("p");
      description.id = `${id}-description`;
      description.textContent = options.description;
      dialog.setAttribute("aria-describedby", description.id);
      form.append(description);
    }
    const label = document.createElement("label");
    label.htmlFor = id;
    label.append("原因 ");
    const mark = document.createElement("span");
    mark.className = "required-mark";
    mark.setAttribute("aria-hidden", "true");
    mark.textContent = "*";
    label.append(mark);
    const input = document.createElement("textarea");
    input.id = id;
    input.name = "reason";
    input.required = true;
    input.setAttribute("aria-required", "true");
    input.rows = 4;
    input.maxLength = 500;
    input.value = options.initialValue || "";
    label.append(input);
    form.append(label);
    const actions = document.createElement("div");
    actions.className = "editor-actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "secondary-button";
    cancel.textContent = "取消";
    const confirm = document.createElement("button");
    confirm.type = "submit";
    confirm.className = "primary-button";
    confirm.textContent = "继续";
    actions.append(cancel, confirm);
    form.append(actions);
    dialog.append(form);
    let finished = false;
    const finish = (value: string | null) => {
      if (finished) return;
      finished = true;
      if (dialog.open) dialog.close();
      dialog.remove();
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus();
      resolve(value);
    };
    input.addEventListener("input", () => input.setCustomValidity(""));
    cancel.addEventListener("click", () => finish(null));
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      finish(null);
    });
    dialog.addEventListener("close", () => finish(null));
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const reason = input.value.trim();
      if (!reason) {
        input.setCustomValidity("请填写原因");
        input.reportValidity();
        return;
      }
      finish(reason);
    });
    document.body.append(dialog);
    dialog.showModal();
    input.focus();
  });
}
