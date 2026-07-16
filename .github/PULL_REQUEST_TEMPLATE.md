## Description

Briefly describe what this PR changes and why.

## Type of change

- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Breaking change (fix or feature that would cause existing behaviour to change)
- [ ] Refactor / cleanup (no behaviour change)
- [ ] Documentation only

## Related issues

Closes #<!-- issue number -->

## Testing

Describe how you tested the change. For bug fixes, include the failing SQL query and expected vs actual output.

- [ ] `make test` passes
- [ ] `make lint` (`go vet`) passes
- [ ] New test added for new behaviour

## Checklist

- [ ] Doc comments added/updated for all exported types and functions changed
- [ ] No new resource leaks (child operators closed on error paths)
- [ ] Error messages are descriptive and include context
