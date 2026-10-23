import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Providers } from "../../App";
import { Dialog } from "./Dialog";

function Host({
  onClose = () => {},
  startClosed = false,
}: {
  onClose?: () => void;
  startClosed?: boolean;
}) {
  const [open, setOpen] = useState(!startClosed);
  return (
    <Providers>
      <button onClick={() => setOpen(true)}>opener</button>
      {open ? (
        <Dialog
          title="Ask something"
          onClose={() => {
            onClose();
            setOpen(false);
          }}
          footer={<button>Last</button>}
        >
          <input aria-label="first field" data-autofocus />
        </Dialog>
      ) : null}
    </Providers>
  );
}

describe("Dialog", () => {
  it("is a modal named by its heading, with the focus on its first field", () => {
    render(<Host />);
    const dlg = screen.getByRole("dialog", { name: "Ask something" });
    expect(dlg).toHaveAttribute("open");
    expect(screen.getByLabelText("first field")).toHaveFocus();
  });

  it("closes on Escape and gives the focus back to the opener", async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<Host onClose={onClose} startClosed />);
    await user.click(screen.getByRole("button", { name: "opener" }));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("button", { name: "opener" })).toHaveFocus();
  });

  it("closes from the close button and from a click on the backdrop", async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    const first = render(<Host onClose={onClose} />);
    await user.click(screen.getByRole("button", { name: "Close dialog" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    first.unmount();

    render(<Host onClose={onClose} />);
    // The backdrop is the <dialog> itself; a click inside the card is not a click on it.
    await user.click(screen.getByRole("heading", { name: "Ask something" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("dialog", { name: "Ask something" }));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("wraps Tab and Shift+Tab inside", async () => {
    const user = userEvent.setup();
    render(<Host />);
    const last = screen.getByRole("button", { name: "Last" });
    last.focus();
    await user.tab();
    expect(screen.getByRole("button", { name: "Close dialog" })).toHaveFocus();
    await user.tab({ shift: true });
    expect(last).toHaveFocus();
  });
});
