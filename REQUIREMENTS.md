# Workflow Engine

This project aim to build a golang based workflow engine. The engine, must be able be used as embedded in golang application or standalone (for example as sidecar or running container), accessible by its rest or grpc API. 

It must adhere to BPMN semantics, expecting it could load from BPMN2 process definition but prefer using yaml or direct golang code. A process definition is template for process intances. Use https://github.com/expr-lang/expr when we need a expression evaluation in process definition and execution.

Use token based execution to model how the engine handle transitions between node inside a process definition. Token must be able to carry specific process instance variables, which migh be used on next node for taking decision what to do next (e.g. exclusive fork gateway decision).

Use sql based database, prefer to use postgresql. Consider to cache data to prevent overloading database, choose hot path access.

For eventing system, please use watermill project (https://github.com/ThreeDotsLabs/watermill), prefer outbox publishing pattern for publishing events. But dont use this directly in workflow related code, need to define an abstraction for eventing system, so we can use other vendor code (no vendor lock in).

For task require scheduled waiter use go-cron (https://github.com/go-co-op/gocron latest version 2.21.2 use as hard pin requirement). For example a human task required by company policy or business process sla to be executed in 3 working days, when it exceed without action from required actor, it must do some alternative action(s), for example sending email then continue to alternative path. We also need action(s) to be executed in between waiting period, for example sending email when the process is waiting for action from human actor. Another example is timer tasks, which will wait for predefined time duration before continue to next process.

Define a catalog of service actions, which can be used in process definition nodes, which can be refer by name in process definition. This service action must be based on a interface.

This library must be able to expose process metrics, enable traces, using slog golang logger.

Need a plugable security authorization mechanism, so we can set required role or resource privilege sets in human task of workflow process definition. Don't limit only role or resource based, we need to be able to evaluate attribute (of data or process variables) based authorization. Consider to use casbin.

In the api surface, we need to be able to customize response of ProcessInstance (now we already have a v1 workflow engine, so we can minimize migration effort).

We need to be able to rollback process to previous node, in case of something wrong in the process execution or in debuging process. So we need an optional plugable compensation action(s) in each node of process definition, in case this happen.

A process error must be able to be retried. Consider also other resilient aspect.

Make it ready for produuction use.

Use golang 1.25, postgresql 17. testcontainer for testing components requiring real access to external resources like database.

We need a feature to support admin or super user to monitor all the process. My opinion, it could be implemented by middleware or sets of http handler.

Do a comprehensive research about best practices of workflow management.
