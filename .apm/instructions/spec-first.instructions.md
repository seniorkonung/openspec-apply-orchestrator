### Spec first

Code is only written to implement an active spec change under openspec/changes/. Code without a driving spec change is invalid, even if all tests pass.

If code changes exist that were not driven by an active spec change, especially when acceptance tests are failing, the correct response is to discard the code and restart from the spec. Patching unspecced code until tests pass is a rule violation, not a fix.
