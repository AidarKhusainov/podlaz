# Accessibility

Podlaz aims to keep its command-line interface usable with keyboard-driven and text-based workflows.

## Supported environments

Podlaz is a terminal application for the systemd-based Linux environments described in the README. Accessibility therefore depends partly on the terminal emulator, shell, operating system, and assistive technology used with it.

## Commitment

When changing user-facing CLI behavior, the project aims to preserve:

- keyboard-only operation;
- meaningful textual status and error output;
- workflows that do not depend solely on color, animation, pointer input, or graphical UI;
- documentation that can be consumed as text.

## Known limitations

Podlaz does not currently claim conformance with a formal accessibility standard.

Terminal and screen-reader behavior can vary across environments. Some networking diagnostics are necessarily dense and technical, and output produced by external system tools may not be under Podlaz's control.

## Reporting accessibility barriers

Use the **Accessibility barrier** issue form and include:

- the command or workflow involved;
- your Linux environment, terminal, and relevant assistive technology;
- what you expected to be able to do;
- what prevented or complicated the task;
- a minimal example or sanitized output when useful.

Do not include credentials, subscription URLs, private endpoints, or other sensitive data.

Accessibility reports are treated as product usability issues and are welcome even when you do not have a proposed fix.
