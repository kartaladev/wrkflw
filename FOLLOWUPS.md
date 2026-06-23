# Follow Up

This document intended for discussion input for development follow up. Some items required for discussion
1. We need to simplify folder or directory structure of project, introduce new `pkg` directory to store all workflow plumbing code. In root project directory must only have workflow related code. Please do deliberately analysis for this.
2. Need to brainstorm for providing a DSL like process-definition builder and `yaml` based worflow definition loader. In my opinion, we need to change `model.Node` to an interface, then implement each workflow node type in a specific concrete type, provide a specific constructor for building them, this is safer to segregate inner state of each node instead of create one-for-all `model.Node` type. Please deliberate analyze this.
3. Process instance must be able to serialized to `json` for complete information. So that front end can render the workflow process history and can make decision what next process need to be done, especially for human task related node.
4. What about renaming `engine` package to `exec`?
5. Remove `BPMN` wording in all golang code docs, deliberate read and update the docs if required to explain semantics of documented code. This inspired by `bpmn` but i don't want consumer assume this is fully bpmn compatible.
6. Write README.md document the project as the last item. Make sure it completes and correct and can be used by every level of developer, both consumer and library maintainer.