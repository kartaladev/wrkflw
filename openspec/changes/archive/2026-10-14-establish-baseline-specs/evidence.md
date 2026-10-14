# Evidence

Scenario-to-test map for every baseline scenario (design D6). The run notes below record the go1.26.8 measurement; the archiving PR re-ran every test in CI on go1.26.9 (kartaladev/wrkflw#243). Each named test ran green on go1.26.8, with Docker for the dialect rows, when the specs were reconciled. Rows marked `yes (characterization)` had no asserting test at baseline and are covered by the characterization tests of tasks group 2.

## definition-store


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`, targeted `-run`. Packages: `internal/persistence/store` (Docker: Postgres and MySQL containers, SQLite in-process), `runtime/kernel`, `runtime`. All ok.

Note: the durable-store rows are exercised through the internal `store.DefinitionStore` (compile-time asserted to satisfy `persistence.DefinitionStore`), not through the public per-dialect facade constructors.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| A published version is immutable and republishing it is idempotent | Republishing identical content is a no-op on every backend | minimal definition x3 backends | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/identical_content_twice_is_an_idempotent_no-op_leaving_one_row | yes |
| A published version is immutable and republishing it is idempotent | Republishing identical content is a no-op on every backend | rich conformance definition x3 backends | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/identical_RICH_content_twice_is_an_idempotent_no-op_leaving_one_row | yes |
| A published version is immutable and republishing it is idempotent | Republishing identical content is a no-op on every backend | scoped actions x3 backends: returns nil, one row | internal/persistence/store:TestDefinitionStoreScopedRepublishIsIdempotent/{postgres,mysql,sqlite} | yes |
| A published version is immutable and republishing it is idempotent | Republishing identical content is a no-op on every backend | scoped actions x3 backends: "reading it back returns the published definition unchanged" | internal/persistence/store:TestBaselineScopedRepublishReadsBackUnchanged/{postgres,mysql,sqlite} (compares ID, Version, Nodes, Flows, CancelActions; the scoped-action catalog is never serialized) | yes (characterization) |
| A published version is immutable and republishing it is idempotent | Different content under a published version is refused | all 3 backends | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/different_content_under_the_same_version_is_refused_and_the_first_survives | yes |
| A published version is immutable and republishing it is idempotent | A stored encoding that differs only in serialisation is not a conflict | all 3 backends | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/a_stored_encoding_differing_only_in_serialisation_is_not_a_conflict (only SQLite actually hands the divergent bytes back; Postgres/MySQL re-serialise, per the test's own comment) | yes |
| A published version is immutable and republishing it is idempotent | The MySQL outcome does not depend on the consumer's DSN flags | false, true | internal/persistence/store:TestDefinitionStorePublishIgnoresClientFoundRows/{default_DSN,clientFoundRows=true} | yes |
| A published version is immutable and republishing it is idempotent | A publish contending with an uncommitted insert resolves on commit | postgres (only row) | internal/persistence/store:TestDefinitionStorePublishUnderContention | yes |
| An invalid definition is refused before anything is written | An invalid definition writes no row on any backend | no nodes x3 | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/a_definition_failing_model.Validate_is_refused_and_writes_no_row | yes |
| An invalid definition is refused before anything is written | An invalid definition writes no row on any backend | version 0 x3 | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/version_0_is_refused_before_any_I/O | yes |
| An invalid definition is refused before anything is written | A valid definition publishes and reads back | all 3 backends | internal/persistence/store:TestDefinitionStorePublishGetRoundTrip/{postgres,mysql,sqlite} | yes |
| Latest resolves to the highest version on every registry | The durable store resolves latest and pinned lookups | all 3 backends | internal/persistence/store:TestDefinitionStoreLookupByQualifier/{postgres,mysql,sqlite} | yes |
| Latest resolves to the highest version on every registry | Registering an older version after a newer one does not demote latest | single | runtime/kernel:TestMemDefinitionRegistryLatestIsHighestVersion | yes |
| Latest resolves to the highest version on every registry | The map registry also resolves latest by highest version | single | runtime/kernel:TestMapDefinitionRegistryLatestResolvesHighestVersion | yes |
| Latest resolves to the highest version on every registry | An in-memory miss is reported | empty registry, Latest("nonexistent") | runtime/kernel:TestMemDefinitionRegistry_LookupMiss | yes |
| Latest resolves to the highest version on every registry | An in-memory miss is reported | Version("order", 9) | runtime/kernel:TestMemRegistryLookupByQualifier | yes |
| Latest resolves to the highest version on every registry | An in-memory miss is reported | empty map registry | runtime/kernel:TestMapDefinitionRegistryLookup/empty_registry_returns_not_found | yes |
| In-memory registration keeps the first registration of a version | A duplicate version with different content is refused | in-memory registry | runtime/kernel:TestBaselineMemRegistryRefusesDifferentContentAndKeepsTheFirst | yes (characterization) |
| In-memory registration keeps the first registration of a version | A duplicate version with different content is refused | process-global, via RegisterDefinition | runtime:TestBaselineRegisterDefinitionRefusesDifferentContentAndKeepsTheFirst | yes (characterization) |
| In-memory registration keeps the first registration of a version | A different version of the same ID is accepted | single | runtime/kernel:TestMemDefinitionRegistryLatestIsHighestVersion (v2 then v1 both register) | yes |
| Message-start names are unique across globally registered definitions | A colliding message-start name is refused | cross-definition | runtime:TestRegisterDefinitionRejectsDuplicateMessageStart/cross-definition_duplicate_message-start_name_is_rejected | yes |
| Message-start names are unique across globally registered definitions | A colliding message-start name is refused | intra-definition | runtime:TestRegisterDefinitionRejectsDuplicateMessageStart/intra-definition_duplicate_message-start_name_is_rejected | yes |
| Message-start names are unique across globally registered definitions | Distinct message-start names register | single | runtime:TestRegisterDefinitionRejectsDuplicateMessageStart/distinct_message-start_names_register_fine | yes |

## definition-validation


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`, targeted `-run`. Packages: `definition/model`, `definition`, `definition/kinds`, `definition/internal/kindreg`, `runtime/kernel`, `runtime`, `internal/persistence/store` (Docker: Postgres and MySQL containers, SQLite in-process). All ok.

Note: the durable-store rows are exercised through the internal `store.DefinitionStore` (which the conformance file asserts satisfies `persistence.DefinitionStore` at compile time), not through the public per-dialect facade constructors.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Building a definition runs structural validation | A builder definition with no start event is refused | Build returns an error | definition/model:TestDefinitionBuilderRejectsInvalid | yes |
| Building a definition runs structural validation | A builder definition with no start event is refused | error matches ErrNoStartEvent; no definition returned | definition/model:TestDefinitionBuilderRejectsInvalid (tightened: errors.Is ErrNoStartEvent, nil definition) | yes (characterization) |
| Building a definition runs structural validation | A YAML definition with no start event is refused at Build | parse succeeds; Build errors | definition/model:TestParseYAMLRejectsInvalid | yes |
| Building a definition runs structural validation | A YAML definition with no start event is refused at Build | error matches ErrNoStartEvent | definition/model:TestParseYAMLRejectsInvalid (tightened: errors.Is ErrNoStartEvent) | yes (characterization) |
| Building a definition runs structural validation | A well-formed definition builds | single | definition/model:TestDefinitionBuilderBuildsAndValidates | yes |
| Registration enforces the authoring gate | Invalid definitions are refused and claim no key | all 4 rows | runtime/kernel:TestMemDefinitionRegistry_ValidatesOnRegister/{no_start_event,version_0_collides_with_the_latest_sentinel,flow_references_unknown_node,intermediate_catch_event_declares_no_trigger} | yes |
| Registration enforces the authoring gate | A valid definition claims both keys | single | runtime/kernel:TestMemDefinitionRegistry_ValidatesOnRegister/valid_definition_is_registered_under_both_keys | yes |
| Registration enforces the authoring gate | The process-global registration path runs the same gate | both rows | runtime:TestRegisterDefinitionValidates/{no_start_event,end_event_has_an_outgoing_flow} | yes |
| Registration enforces the authoring gate | The panicking variants inherit the gate | MustRegister on an in-memory registry | runtime/kernel:TestMemDefinitionRegistry_MustRegisterPanicsOnInvalidDefinition | yes |
| Registration enforces the authoring gate | The panicking variants inherit the gate | MustRegisterDefinition | runtime:TestMustRegisterDefinitionPanics/definition_fails_model.Validate | yes |
| Registration enforces the authoring gate | Nil and unnamed definitions are refused bare | nil definition | runtime/kernel:TestMemDefinitionRegistry_NilDef | yes |
| Registration enforces the authoring gate | Nil and unnamed definitions are refused bare | empty ID | runtime/kernel:TestMemDefinitionRegistry_EmptyID | yes |
| Identifier and version are bounded to the storable domain | Bounds and their accepting neighbours | all 9 rows | runtime/kernel:TestValidateDefinitionEnforcesTheStorableDomain/* | yes |
| Identifier and version are bounded to the storable domain | The durable store applies the same bounds on every backend | all 12 rows | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/{a_definition_ID_one_rune_over_the_limit_is_refused_and_writes_no_row,a_definition_ID_that_is_not_valid_UTF-8_is_refused_and_writes_no_row,a_definition_ID_containing_a_NUL_byte_is_refused_and_writes_no_row,a_version_one_over_the_backend_maximum_is_refused_and_writes_no_row} | yes |
| Identifier and version are bounded to the storable domain | Values at the bound are stored faithfully on every backend | all 9 rows | internal/persistence/store:TestDefinitionStorePublishImmutable/{postgres,mysql,sqlite}/{a_definition_ID_at_the_length_limit_is_accepted,a_definition_ID_of_valid_multibyte_UTF-8_is_accepted,a_version_at_the_backend_maximum_is_accepted} | yes |
| Structural authoring defects are refused | Each structural defect is refused by its sentinel | dead end | definition/model:TestValidate/dead_end_non-end_node | yes |
| Structural authoring defects are refused | Each structural defect is refused by its sentinel | dangling target, dangling source | definition/model:TestValidate/{dangling_flow_target,dangling_flow_source} | yes |
| Structural authoring defects are refused | Each structural defect is refused by its sentinel | two "charge" nodes; two "dup" nodes in a subprocess | definition/model:TestValidate/{duplicate_node_ID_is_rejected,duplicate_node_ID_inside_a_sub-process_is_rejected_(recursion)} | yes |
| Structural authoring defects are refused | Each structural defect is refused by its sentinel | two blank-ID nodes | definition/model:TestBaselineValidateRefusesTwoBlankNodeIDs | yes (characterization) |
| Structural authoring defects are refused | Each structural defect is refused by its sentinel | subProcess with no nested definition | definition/model:TestValidateSubProcess/subprocess_with_nil_Subprocess_pointer_is_invalid | yes |
| Structural authoring defects are refused | The legal neighbours validate | linear | definition/model:TestValidate/valid_linear | yes |
| Structural authoring defects are refused | The legal neighbours validate | reused IDs in nested subprocess | definition/model:TestValidate/an_ID_reused_across_the_outer_and_a_nested_definition_is_accepted | yes |
| Structural authoring defects are refused | The legal neighbours validate | valid nested definition | definition/model:TestValidateSubProcess (first row, valid subprocess) | yes |
| Intermediate events must declare what they wait for or emit | Trigger-less intermediate events are refused | 3 catch rows | definition/model:TestValidate_RejectsCatchEventWithoutTrigger/{catch_with_no_trigger_at_all_is_rejected,catch_with_only_a_correlation_key_is_rejected,trigger-less_catch_behind_an_event_gateway_is_rejected_too} | yes |
| Intermediate events must declare what they wait for or emit | Trigger-less intermediate events are refused | 2 throw rows | definition/model:TestValidate_RejectsThrowEventWithoutTrigger/{throw_with_no_signal_name_is_rejected,a_display_name_does_not_rescue_a_throw_that_emits_nothing} | yes |
| Intermediate events must declare what they wait for or emit | Events that declare a trigger validate | 3 catch rows | definition/model:TestValidate_RejectsCatchEventWithoutTrigger/{timer_catch_stays_valid,signal_catch_stays_valid,message_catch_stays_valid} | yes |
| Intermediate events must declare what they wait for or emit | Events that declare a trigger validate | signal throw, compensation throw | definition/model:TestValidate_RejectsThrowEventWithoutTrigger/{signal_throw_stays_valid,compensation_throw_is_a_different_kind_and_stays_valid} | yes |
| Trigger boundaries attach only to hosts that arm them | A trigger boundary on an unarmed host is refused | all 3 rows | definition/model:TestValidate_RejectsTriggerBoundaryOnUnarmedHost/{timer_boundary_on_a_send_task_is_rejected,signal_boundary_on_a_sub-process_is_rejected,message_boundary_on_a_call_activity_is_rejected} | yes |
| Trigger boundaries attach only to hosts that arm them | Armed hosts and error boundaries stay valid | all 6 rows | definition/model:TestValidate_RejectsTriggerBoundaryOnUnarmedHost/*_stays_valid | yes |
| A node of a foreign type is refused | A counterfeit node is refused at both ingresses | all 5 rows | definition:TestForeignNodeRejectedAtEveryGatedIngress/{top_level,nested_under_KindStartEvent,depth_two,ValidationGet_kind_receiveTask,ValidationGet_kind_intermediateCatchEvent}/{model.Validate,Builder.Build} | yes |
| A node of a foreign type is refused | Every genuine kind passes the type check | single | definition/kinds:TestRecordedNodeTypeMatchesFromWire | yes |
| Node kinds are closed to consumers | A consumer cannot reach kind registration | both rows | definition/internal/kindreg:TestRegistrationIsSealedAgainstConsumers/{a_consumer_cannot_import_the_token_package,a_consumer_cannot_forge_a_token_to_reach_RegisterKind} (ran, not skipped) | yes |
| Node kinds are closed to consumers | The shipped kind packages do register every kind | single | definition/kinds:TestAllKindsRegistered | yes |
| A businessRuleTask cannot carry both a rule and an action | Rule and action together are refused | single | definition/model:TestValidate_RejectsReservedRule/rule_and_action_together_names_the_exclusivity_defect | yes |
| A businessRuleTask cannot carry both a rule and an action | A businessRuleTask without a rule validates | both rows | definition/model:TestValidate_RejectsReservedRule/{a_businessRuleTask_with_an_action_and_no_rule_stays_valid,a_bare_businessRuleTask_stays_valid,_action_defaulting_to_the_id} | yes |
| Declarative validation kinds reconstruct, unregistered kinds fail Build | An expr validation block reconstructs at Build | both rows | definition/model:TestLoader_WithValidatorRegistry_ReconstructsStrategy/{registered_kind_reconstructs_and_validates_identically,no_explicit_registry_falls_back_to_DefaultRegistry} | yes |
| Declarative validation kinds reconstruct, unregistered kinds fail Build | An unregistered kind fails Build | single | definition/model:TestLoader_WithValidatorRegistry_ReconstructsStrategy/unregistered_kind_surfaces_unknown_kind | yes |
| Declarative validation kinds reconstruct, unregistered kinds fail Build | A persisted expr validation reconstructs on decode | single | definition/model:TestDurableReload_ReconstructsViaDefaultRegistry/registered_kind_reconstructs_live_and_validates (also TestStartValidation_WireRoundTrip) | yes |
| Declarative validation kinds reconstruct, unregistered kinds fail Build | A json-schema strategy round-trips through its descriptor | single | definition/model/validate/jsonschema:TestBaselineJSONSchemaRoundTripsThroughItsDescriptor | yes (characterization) |

## definition-wire-format


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`, targeted `-run`. Packages: `definition/model` (ok), `definition/kinds` (ok).

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Node kinds are encoded by stable name | Every kind encodes as its name and round-trips | all 8 rows | definition/model:TestNodeKindJSONMarshal (covers all 17 kinds) | yes |
| Node kinds are encoded by stable name | An unknown or retired kind name is refused | notANodeKind: decoding fails | definition/model:TestNodeKindJSONUnmarshalUnknown | yes |
| Node kinds are encoded by stable name | An unknown or retired kind name is refused | notANodeKind: error names the value | definition/model:TestBaselineNodeKindDecodeNamesTheRefusedValue/unknown_name | yes (characterization) |
| Node kinds are encoded by stable name | An unknown or retired kind name is refused | terminateEndEvent: decoding fails | definition/model:TestNodeKindTerminateEndRetired | yes |
| Node kinds are encoded by stable name | An unknown or retired kind name is refused | terminateEndEvent: error names the value | definition/model:TestBaselineNodeKindDecodeNamesTheRefusedValue/retired_terminate_end_name | yes (characterization) |
| Node kinds are encoded by stable name | An unknown or retired kind name is refused | errorEndEvent (fails and names it) | definition/model:TestRetiredErrorEndWireName | yes |
| Node kinds are encoded by stable name | A kind inside a definition is written as a name | single | definition/model:TestNodeKindJSONInNode | yes |
| Node kinds are encoded by stable name | YAML refuses an unknown kind name | single | definition/model:TestBaselineParseYAMLRefusesAnUnknownKindName | yes (characterization) |
| Unknown keys are refused at decode | An unknown key is refused in either format | JSON rows (bogus_top, bogus_node_key, eligable_roles) | definition/model:TestProcessDefinitionUnmarshalJSONRejectsUnknownFields/{unknown_key_at_top_level,unknown_key_inside_a_node,misspelled_eligible_roles_is_rejected,_not_silently_dropped} | yes |
| Unknown keys are refused at decode | An unknown key is refused in either format | YAML rows (bogus_top_level, eligable_roles, bogus_flow_key) | definition/model:TestParseYAMLRejectsUnknownFields/{unknown_key_at_top_level,misspelled_eligible_roles_on_a_node,unknown_key_inside_a_flow} | yes |
| Unknown keys are refused at decode | The unmodified definition still decodes | JSON | definition/model:TestProcessDefinitionUnmarshalJSONRejectsUnknownFields/valid_definition_still_decodes | yes |
| Unknown keys are refused at decode | The unmodified definition still decodes | YAML | definition/model:TestParseYAMLRejectsUnknownFields/valid_definition_still_parses | yes |
| The retired label key is refused with a pointer to name | Any label key is refused | 5 JSON rows | definition/model:TestNodeWireRetiredLabelKey (5 refusal subtests) | yes |
| The retired label key is refused with a pointer to name | Any label key is refused | 3 YAML rows | definition/model:TestParseYAMLRetiredLabelKey/{explicit_label_is_refused,_naming_name,an_empty_label_value_is_still_the_retired_key,a_null_label_value_is_still_the_retired_key} | yes |
| The retired label key is refused with a pointer to name | Name alone is accepted as the display string | JSON | definition/model:TestNodeWireRetiredLabelKey/name_with_no_label_decodes,_and_Name_is_the_display_string | yes |
| The retired label key is refused with a pointer to name | Name alone is accepted as the display string | YAML | definition/model:TestParseYAMLRetiredLabelKey/name_with_no_label_parses,_and_Name_is_the_display_string | yes |
| The retired label key is refused with a pointer to name | An unrelated unknown key is not reported as label | JSON | definition/model:TestNodeWireRetiredLabelKey/an_unrelated_unknown_key_still_names_that_key,_not_label | yes |
| The retired label key is refused with a pointer to name | An unrelated unknown key is not reported as label | YAML | definition/model:TestParseYAMLRetiredLabelKey/an_unrelated_unknown_key_still_names_that_key,_not_label | yes |
| The retired label key is refused with a pointer to name | Encoding never writes label | single | definition/model:TestNodeWireNeverEmitsLabel | yes |
| A key the node kind never reads is refused | A misplaced key is refused in both formats | all 15 rows (JSON and YAML) | definition/model:TestKindGateRefusesAKeyTheKindCannotCarry/{json,yaml}/* (refusedKey asserts key, kind and node id "n1") | yes |
| A key the node kind never reads is refused | A key the kind reads is accepted | all rows except JSON timer_trigger | definition/model:TestKindGateRefusesAKeyTheKindCannotCarry/{json,yaml}/* (acceptedKey rows) | yes |
| A key the node kind never reads is refused | A key the kind reads is accepted | JSON intermediateCatchEvent timer_trigger | definition/model:TestKindGateRefusesAKeyTheKindCannotCarry/json/timer_trigger_on_an_intermediateCatchEvent | yes |
| A key the node kind never reads is refused | A zero value under a misplaced key is asymmetric | all 6 rows | definition/model:TestZeroValuedMisplacedKeyIsAsymmetric/* (drives the decode gate function directly, not through a decoder) | yes |
| A key the node kind never reads is refused | The legacy flat timer form is accepted and normalised | single | definition/model:TestKindGateKeepsTheLegacyTimerRelocation | yes |
| A key the node kind never reads is refused | The refusal reaches a nested subprocess | JSON terminate (accepted) | definition/model:TestKindGateReachesIntoANestedSubprocess/a_legal_inner_node_survives | yes |
| A key the node kind never reads is refused | The refusal reaches a nested subprocess | JSON action (refused, names i_end and action) | definition/model:TestKindGateReachesIntoANestedSubprocess/a_misplaced_key_on_an_inner_node_is_refused | yes |
| A key the node kind never reads is refused | The refusal reaches a nested subprocess | YAML action | definition/model:TestKindGateReachesIntoANestedSubprocess/YAML_reaches_the_same_depth | yes |
| termination_outcome on a terminate end is refused outside its vocabulary | Out-of-vocabulary values are refused | all 8 rows | definition/model:TestTerminationOutcomeVocabularyAtDecode/{wrong_case,shouted,transposed_letters,trailing_space}/{json,yaml} | yes |
| termination_outcome on a terminate end is refused outside its vocabulary | The refusal locates the node and quotes the value | JSON, YAML | definition/model:TestTerminationOutcomeVocabularyAtDecode/the_diagnostic_names_the_node,_the_value_and_the_vocabulary/{json,yaml} | yes |
| termination_outcome on a terminate end is refused outside its vocabulary | The vocabulary values are accepted as themselves | all 6 rows | definition/model:TestTerminationOutcomeVocabularyAtDecode/{abort,complete,empty_is_the_unauthored_outcome}/{json,yaml} | yes |
| The reserved rule key refuses scalars that name nothing | Malformed scalar rule values are refused | 4 JSON rows | definition/model:TestRuleSpecUnmarshalJSON/{a_number_is_refused,a_bool_is_refused,an_empty_name_is_refused,a_blank_name_is_refused} | yes |
| The reserved rule key refuses scalars that name nothing | Malformed scalar rule values are refused | 3 YAML rows | definition/model:TestRuleSpecUnmarshalYAML/{an_int_is_refused,a_bool_is_refused,a_blank_name_is_refused} | yes |
| The reserved rule key refuses scalars that name nothing | A non-blank name is accepted | JSON "pricing.v3" | definition/model:TestRuleSpecUnmarshalJSON/a_string_is_a_catalog_name | yes |
| The reserved rule key refuses scalars that name nothing | A non-blank name is accepted | YAML pricing.v3, quoted "true" | definition/model:TestRuleSpecUnmarshalYAML/{a_plain_scalar_is_a_catalog_name,a_quoted_bool-looking_scalar_is_still_a_name} | yes |
| The reserved rule key refuses scalars that name nothing | An explicit null rule means no rule | JSON, YAML | definition/model:TestNodeRuleNullMeansAbsent/{JSON,YAML} | yes |
| A kind with no registered node type fails loudly | An unregistered kind is refused | single | definition/model:TestFromWireUnregisteredKindIsLoud | yes |
| A kind with no registered node type fails loudly | Every shipped kind is registered once the kinds bundle is imported | single | definition/kinds:TestAllKindsRegistered | yes |
| Encoding refuses a definition that could not round-trip | A foreign node is refused at any depth | all 3 rows | definition/kinds:TestMarshalJSONRejectsForeignNodeType (top level, one level down, two levels down; asserts nil output) | yes |
| Encoding refuses a definition that could not round-trip | The genuine node type still encodes at depth | single | definition/kinds:TestMarshalJSONRejectsForeignNodeType/the_real_leaf_type_under_its_own_kind_still_marshals_cleanly,_at_depth_two | yes |
| Encoding refuses a definition that could not round-trip | A callback validation strategy is refused | single | definition/model:TestMarshalJSON_ValidationStrategyFailClosed/callback_strategy_fails_closed | yes |
| Encoding refuses a definition that could not round-trip | A declarative validation strategy encodes | single | definition/model:TestMarshalJSON_ValidationStrategyFailClosed/describable_strategy_marshals | yes |
| Library-encoded definitions decode back unchanged | The golden definition round-trips byte for byte | single | definition/kinds:TestGoldenRoundTrip | yes |
| Library-encoded definitions decode back unchanged | No library output is refused by the key gate | widest wire form | definition/model:TestNoWireOutputIsRefusedByTheKindGate | yes |
| Library-encoded definitions decode back unchanged | No library output is refused by the key gate | Go-constructed | definition/model:TestNoGoConstructedNodeIsRefusedByTheKindGate | yes |
| Library-encoded definitions decode back unchanged | A definition loaded from YAML survives a store round trip | single | definition/model:TestPersistedDefinitionRoundTripsThroughStrictJSON | yes |

## event-delivery


All named tests run with `GOTOOLCHAIN=go1.26.8 go test -count=1 -run ...` (Docker up). `store:` = `internal/persistence/store`, the shared relay the `persistence` per-dialect relay constructors delegate to.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Eventing names no third-party messaging library | Direct imports stay within the allow-list | all | eventing:TestEventingDependencyGraphNamesNoVendorDirectly | yes |
| Eventing names no third-party messaging library | No denied module appears transitively | watermill, oklog/ulid, lithammer/shortuuid, pkg/errors | eventing:TestEventingDependencyGraphIsMessagingVendorFree | yes |
| Published envelopes carry the outbox wire contract | A fully populated outbox event maps to the golden envelope | all | eventing:TestPublisherMapsOutboxEventToEnvelope/golden_envelope_for_a_fully_populated_event | yes |
| Published envelopes carry the outbox wire contract | An event without a dedup key still gets a unique envelope id | all | eventing:TestPublisherMapsOutboxEventToEnvelope/an_event_with_no_dedup_key_gets_a_freshly_minted,_unique_id | yes |
| Publish failures are returned to the caller | Failure outcomes of a publish | not JSON-encodable | eventing:TestPublisherMapsOutboxEventToEnvelope/an_unmarshalable_payload_fails_before_the_PublishFunc_runs | yes |
| Publish failures are returned to the caller | Failure outcomes of a publish | sentinel error | eventing:TestPublisherMapsOutboxEventToEnvelope/a_PublishFunc_failure_is_wrapped_with_the_topic_and_recorded_on_the_span | yes |
| Publish failures are returned to the caller | Failure outcomes of a publish | nil / nil (calls 1) | eventing:TestPublisherMapsOutboxEventToEnvelope/golden_envelope_for_a_fully_populated_event | yes |
| Trace context crosses the broker in the envelope | The default propagator writes traceparent | all | eventing:TestPublishInjectsTraceContext/the_default_propagator_writes_a_W3C_traceparent_naming_the_publish_span | yes |
| Trace context crosses the broker in the envelope | An explicit propagator replaces the default | all | eventing:TestPublishInjectsTraceContext/an_explicit_propagator_replaces_the_default | yes |
| Trace context crosses the broker in the envelope | A handler span parents onto the publish span | all | eventing:TestPublishSpanParentsHandlerSpan | yes |
| The in-process bus fans out to every subscription on the topic | Every subscriber on the topic receives the envelope | all | eventing:TestInProcessFansOutToEverySubscriber | yes |
| The in-process bus fans out to every subscription on the topic | Each subscription receives its own topic's envelopes | all (snapshot-level; the never-event is not provable, see test doc) | eventing:TestInProcessDeliversOnlyToTheMatchingTopic | yes |
| Each in-process subscription delivers in publish order | A queued backlog drains first-in first-out | all | eventing:TestInProcessDeliversAQueuedBacklogInPublishOrder | yes |
| A handler's return value is its ack or nack | A nacked envelope is redelivered after the backoff | all | eventing:TestInProcessRedeliversNackedEnvelopesAfterBackoff | yes |
| Every in-process delivery attempt receives its own copy | Sibling subscriptions are isolated | all | eventing:TestInProcessGivesEachSubscriptionItsOwnEnvelope | yes |
| Every in-process delivery attempt receives its own copy | A redelivery is isolated from the previous attempt | all | eventing:TestInProcessGivesEachDeliveryAttemptItsOwnEnvelope | yes |
| Publishing to a topic whose subscriptions have all ended is reported | Publish after the last subscription stopped | all | eventing:TestPublishAfterTheLastSubscriptionStoppedIsReported | yes |
| Publishing to a topic whose subscriptions have all ended is reported | Negative control - a topic never subscribed stays silent | all | eventing:TestPublishToATopicNobodyEverSubscribedStaysSilent | yes |
| Publishing to a topic whose subscriptions have all ended is reported | Negative control - a live subscription is not refused | all | eventing:TestInProcessFansOutToEverySubscriber | yes |
| Closing the in-process bus ends subscriptions and refuses later publishes | Close ends idle subscriptions and Subscribe returns nil | all | eventing:TestInProcessCloseEndsEverySubscription | yes |
| Closing the in-process bus ends subscriptions and refuses later publishes | Close ends a subscription parked in the redelivery backoff | all | eventing:TestInProcessCloseEndsASubscriptionParkedInTheRedeliveryBackoff | yes |
| Closing the in-process bus ends subscriptions and refuses later publishes | Close cancels the context of a Start subscription | all | eventing:TestInProcessCloseCancelsTheContextsItCreated | yes |
| Closing the in-process bus ends subscriptions and refuses later publishes | Calls after Close | Publish, a second Close | eventing:TestInProcessCloseEndsEverySubscription; eventing:TestPublishToAClosedBusLogsAtDebugNotError | yes |
| Closing the in-process bus ends subscriptions and refuses later publishes | Calls after Close | Subscribe, Start | eventing:TestBaselineInProcessRefusesSubscriptionsAfterClose/{Subscribe,Start} | yes (characterization) |
| Closing the in-process bus ends subscriptions and refuses later publishes | Negative control - an open bus accepts a publish | all | eventing:TestInProcessFansOutToEverySubscriber | yes |
| Ending a subscription stops at the next envelope boundary | A cancelled subscription abandons its backlog | all | eventing:TestInProcessAbandonsItsBacklogWhenTheSubscriptionEnds | yes |
| Ending a subscription stops at the next envelope boundary | Subscribe returns the cancellation error | all | eventing:TestInProcessUnregistersOnEveryExitPath/Subscribe_returning_on_its_context's_cancellation | yes |
| InProcess.Start returns only once the subscription is live | A publish immediately after Start is delivered | all | eventing:TestInProcessFansOutToEverySubscriber; eventing:TestInProcessUnregistersOnEveryExitPath/Start's_stop_function_joining_the_delivery_loop | yes |
| InProcess.Start returns only once the subscription is live | stop waits for an in-flight handler | all | eventing:TestInProcessStopJoinsItsDeliveryLoop | yes |
| InProcess.Start returns only once the subscription is live | stop is safe twice and concurrently | all | eventing:TestInProcessStopIsSafeToCallTwiceAndConcurrently | yes |
| The message handler routes decodable messages and acks poison | Message handler decisions | valid body | eventing:TestNewMessageHandler/a_decodable_message_routes_to_deliver | yes |
| The message handler routes decodable messages and acks poison | Message handler decisions | {not json | eventing:TestNewMessageHandler/a_malformed_payload_is_acked_and_never_delivered | yes |
| The message handler routes decodable messages and acks poison | Message handler decisions | no messageName | eventing:TestNewMessageHandler/an_empty_message_name_is_acked_and_ignored | yes |
| The message handler routes decodable messages and acks poison | Message handler decisions | (empty) | eventing:TestNewMessageHandler/an_empty_body_is_acked_and_ignored | yes |
| The message handler routes decodable messages and acks poison | Message handler decisions | deliver fails | eventing:TestNewMessageHandler/a_transient_deliver_failure_is_returned_so_the_envelope_is_nacked | yes |
| The message handler routes decodable messages and acks poison | The routed values reach deliver unchanged | all | eventing:TestNewMessageHandler/a_decodable_message_routes_to_deliver | yes |
| The relay publishes pending outbox rows and marks them published | A drain publishes every pending row on every dialect | Postgres/MySQL/SQLite, 3 rows | store:TestRelayDrainOnce_PublishesAndMarks/{postgres,mysql,sqlite} | yes |
| The relay publishes pending outbox rows and marks them published | A drain publishes every pending row on every dialect | Postgres/MySQL/SQLite, 0 rows (returns 0) | store:TestRelayDrainOnce_EmptyOutbox/{postgres,mysql,sqlite} | yes |
| The relay publishes pending outbox rows and marks them published | A drain publishes every pending row on every dialect | all rows through the persistence constructors (pending count, second drain) | persistence:TestBaselineRelayDrainsEveryPendingRowViaFacade/{3_rows,0_rows}/{postgres,mysql,sqlite} | yes (characterization) |
| The relay publishes pending outbox rows and marks them published | The relay built through the persistence package drains | Postgres, 1 | persistence:TestNewRelayDrainsOutbox | yes |
| The relay publishes pending outbox rows and marks them published | The relay built through the persistence package drains | MySQL, 3 | persistence:TestNewMySQLRelay_DrainsViaFacade | yes |
| The relay publishes pending outbox rows and marks them published | The relay built through the persistence package drains | SQLite, 3 | persistence:TestNewSQLiteRelay_DrainsViaFacade | yes |
| A failing outbox row is retried, isolated and finally dead-lettered | Poison isolation and quarantine on every dialect | Postgres, MySQL, SQLite | store:TestRelayPoisonIsolation/{postgres,mysql,sqlite} | yes |
| A failing outbox row is retried, isolated and finally dead-lettered | Poison isolation and quarantine on every dialect | all, through the persistence constructors | persistence:TestBaselineRelayIsolatesAndQuarantinesAPoisonRowViaFacade/{postgres,mysql,sqlite} | yes (characterization) |
| A failing outbox row is retried, isolated and finally dead-lettered | A failed row is not claimed before its backoff elapses | Postgres, MySQL, SQLite | persistence:TestBaselineRelayDefersAFailedRowUntilItsBackoffElapses/{postgres,mysql,sqlite} | yes (characterization) |
| A failing outbox row is retried, isolated and finally dead-lettered | A publish failure is absorbed, not propagated | Postgres, MySQL, SQLite | store:TestRelayDrainOnce_PublishError_Retry/{postgres,mysql,sqlite} | yes |
| A failing outbox row is retried, isolated and finally dead-lettered | A publish failure is absorbed, not propagated | all, through the persistence constructors | persistence:TestBaselineRelayAbsorbsAPublishFailureViaFacade/{postgres,mysql,sqlite} | yes (characterization) |
| Relay.Run keeps polling through publish failures and stops on cancellation | Run absorbs failures and exits on cancel on every dialect | Postgres, MySQL, SQLite | store:TestRelayRun_AbsorbsPublishFailures/{postgres,mysql,sqlite} | yes |
| Relay.Run keeps polling through publish failures and stops on cancellation | Run absorbs failures and exits on cancel on every dialect | all, through the persistence constructors | persistence:TestBaselineRelayRunAbsorbsFailuresAndExitsOnCancelViaFacade/{postgres,mysql,sqlite} | yes (characterization) |
| Dead-lettered rows can be listed and redriven | Listing and redriving on every dialect | Postgres, MySQL, SQLite | store:TestRelayListDeadLettered/{postgres,mysql,sqlite}; store:TestRelayRedrive/{postgres,mysql,sqlite} | yes |
| Dead-lettered rows can be listed and redriven | Listing and redriving on every dialect | Postgres through persistence: empty list and Redrive with no ids only | persistence:TestNewRelayDLQAdminViaFacade | yes |
| Dead-lettered rows can be listed and redriven | Listing and redriving on every dialect | dead/pending rows through the persistence constructors (all dialects); MySQL and SQLite at all | persistence:TestBaselineRelayListsAndRedrivesDeadRowsViaFacade/{postgres,mysql,sqlite} | yes (characterization) |
| The relay reports outbox backlog statistics | Stats on every dialect | Postgres, MySQL, SQLite | store:TestRelayOutboxStats/{postgres,mysql,sqlite} | yes |
| The relay reports outbox backlog statistics | Stats on every dialect | SQLite through persistence: Pending only (Dead=0, no age) | persistence:TestNewSQLiteRelay_OutboxStatsViaFacadeInterface | yes |
| The relay reports outbox backlog statistics | Stats on every dialect | Dead=1 and oldest age through the persistence constructors; Postgres and MySQL at all | persistence:TestBaselineRelayOutboxStatsViaFacade/{postgres,mysql,sqlite} | yes (characterization) |
| Postgres can wake the relay by notification; other dialects poll | Postgres drains on notification before the poll interval | all (shared relay with the pgx notifier) | store:TestPgxNotifierListenDrainsBeforePollInterval | yes |
| Postgres can wake the relay by notification; other dialects poll | Postgres drains on notification before the poll interval | through persistence.WithOutboxNotify + WithListenNotify | persistence:TestBaselineRelayDrainsOnNotificationViaFacade (Postgres) | yes (characterization) |
| Postgres can wake the relay by notification; other dialects poll | MySQL and SQLite still drain by polling | MySQL, SQLite | store:TestRelayRun_MySQL_SQLite_StillPoll/{mysql,sqlite} | yes |
| Postgres can wake the relay by notification; other dialects poll | MySQL and SQLite still drain by polling | through the persistence constructors | persistence:TestBaselineRelayDrainsByPollingViaFacade/{mysql,sqlite} | yes (characterization) |

## expression-evaluation


Tests ran with `GOTOOLCHAIN=go1.26.8 go test ./engine ./runtime ./internal/expreval ./definition/model/validate/expr -run ... -count=1`. "naming" clauses: the code puts the expression (or predicate) text in every such error, and each naming clause is asserted by its own characterization row (task 2.7).

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Instance data is bound as data, never evaluated as expression source | Expression-shaped error text does not satisfy a boundary equality | " or true or " | engine:TestBoundaryRoutingUnderCallerChosenKeyNames/DOES_NOT_REPRODUCE:_a_key_name_that_is_expr_source_is_not_evaluated_as_expr_source | yes |
| Instance data is bound as data, never evaluated as expression source | Expression-shaped error text does not satisfy a boundary equality | == 'x' or true or | engine:TestBoundaryRoutingUnderCallerChosenKeyNames/DOES_NOT_REPRODUCE:_a_key_name_that_is_a_complete,_well-typed_expr_fragment | yes |
| Instance data is bound as data, never evaluated as expression source | Expression-shaped error text does not satisfy a boundary equality | PAY_ERR | engine:TestBoundaryRoutingUnderCallerChosenKeyNames/DOES_NOT_REPRODUCE:_a_key_name_equal_to_the_awaited_code_cannot_satisfy_an_equality | yes |
| Instance data is bound as data, never evaluated as expression source | Expression-shaped variable text does not satisfy a gateway condition | - | engine:TestErrorMessageReachabilityAndGatewayRouting/DOES_NOT_REPRODUCE:_a_key_name_that_is_expr_source_is_not_evaluated_as_expr_source | yes |
| Instance data is bound as data, never evaluated as expression source | The same boundary still matches the data it asks for | - | engine:TestBoundaryRoutingUnderCallerChosenKeyNames/at-limit_accept_for_the_two_refusals:_this_message_shape_does_route_when_the_predicate_asks_for_it (S = "unknown key") | yes |
| A condition that is not a valid boolean expression is an error | Invalid conditions are errors | amount > (error) | internal/expreval:TestEvalBool/syntax_error | yes |
| A condition that is not a valid boolean expression is an error | Invalid conditions are errors | amount + 1 (error) | internal/expreval:TestEvalBool/non-bool_result_errors | yes |
| A condition that is not a valid boolean expression is an error | Invalid conditions are errors | both rows: error names the condition | internal/expreval:TestBaselineExpressionRefusalNamesTheExpression/{condition_that_does_not_compile,condition_with_a_non-boolean_result} | yes (characterization) |
| A condition that is not a valid boolean expression is an error | A valid boolean condition evaluates | amount 150 | internal/expreval:TestEvalBool/true_comparison | yes |
| A condition that is not a valid boolean expression is an error | A valid boolean condition evaluates | amount 50 | internal/expreval:TestEvalBool/false_comparison | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | "3h" | internal/expreval:TestEvalDuration/string_3h | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | "90s" | internal/expreval:TestEvalDuration/string_90s | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | 90 | internal/expreval:TestEvalDuration/integer_90_interpreted_as_seconds | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | slaSeconds | internal/expreval:TestEvalDuration/env-driven_int_slaSeconds | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | int8 5 | internal/expreval:TestEvalDuration/env-driven_int8 | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | uint64 17 | internal/expreval:TestEvalDuration/env-driven_uint64 | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | 2h duration | internal/expreval:TestEvalDuration/env-driven_time.Duration_used_as-is | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | 1.5 | internal/expreval:TestEvalDuration/float64_fractional_seconds_(1.5_=>_1500ms) | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | true (error) | internal/expreval:TestEvalDuration/bool_result_errors | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | "xyz" (error) | internal/expreval:TestEvalDuration/unparseable_string_errors | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | duration > (error) | internal/expreval:TestEvalDuration/syntax_error | yes |
| Duration expressions accept durations, numbers of seconds and duration strings | Duration by result form | three error rows: error names the expression | internal/expreval:TestBaselineExpressionRefusalNamesTheExpression/{duration_with_a_boolean_result,duration_with_an_unparseable_string_result,duration_that_does_not_compile} | yes (characterization) |
| Correlation key expressions render to strings | Correlation key by result form | (empty) | internal/expreval:TestEvalString/empty_code_returns_empty_string_(fast_path) | yes |
| Correlation key expressions render to strings | Correlation key by result form | "hello" | internal/expreval:TestEvalString/string_literal | yes |
| Correlation key expressions render to strings | Correlation key by result form | orderId | internal/expreval:TestEvalString/string_variable | yes |
| Correlation key expressions render to strings | Correlation key by result form | id 99 | internal/expreval:TestEvalString/int_variable_formats_to_string | yes |
| Correlation key expressions render to strings | Correlation key by result form | flag true | internal/expreval:TestEvalString/bool_variable_formats_to_string | yes |
| Correlation key expressions render to strings | Correlation key by result form | orderId > (error) | internal/expreval:TestEvalString/syntax_error | yes |
| Correlation key expressions render to strings | Correlation key by result form | orderId > : error names the expression | internal/expreval:TestBaselineExpressionRefusalNamesTheExpression/correlation_key_that_does_not_compile | yes (characterization) |
| Expression evaluation is bounded on opt-in | A runaway expression is abandoned under the opt-in timeout | exclusive gateway condition | runtime:TestProcessDriverWithExpressionTimeoutGuardsGateway | yes |
| Expression evaluation is bounded on opt-in | A runaway expression is abandoned under the opt-in timeout | timer duration | runtime:TestBaselineExpressionTimeoutThroughDriver/timer_duration | yes (characterization) |
| Expression evaluation is bounded on opt-in | A runaway expression is abandoned under the opt-in timeout | message correlation key | runtime:TestBaselineExpressionTimeoutThroughDriver/message_correlation_key | yes (characterization) |
| Expression evaluation is bounded on opt-in | A normal condition evaluates under the default driver | - | runtime:TestProcessDriverDefaultEvaluatesNormallyAndStaysPure | yes |
| The expr validation kind requires every predicate to hold | Validation outcome by input | accepted row | definition/model/validate/expr:TestExpr_ValidateAndRoundTrip | yes |
| The expr validation kind requires every predicate to hold | Validation outcome by input | "maybe" refused, missing amount refused | definition/model/validate/expr:TestExpr_ValidateAndRoundTrip (asserts err != nil only) | yes |
| The expr validation kind requires every predicate to hold | Validation outcome by input | both refusals: error names the failing predicate | definition/model/validate/expr:TestBaselineExprValidationNamesFailingPredicate | yes (characterization) |

## http-transport


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`. Task-route identity rows moved to human-task-authorization (see that evidence file).

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | stdlib POST /instances 201 | transport/http/stdlib:TestMount_StartInstance (bare Mount, no actor) | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | gin POST /instances 201 | transport/http/gin:TestInstanceRoutes_StartInstance_201 (bare mount) | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | fiber POST /instances 201 | transport/http/fiber:TestMount_StartInstance (bare Mount) | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | stdlib POST /instances/{id}/signals 200 | transport/http/stdlib:TestDeliverSignal_Stdlib | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | gin POST /instances/{id}/signals 200 | transport/http/gin:TestInstanceRoutes_DeliverSignal_200 | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | fiber POST /instances/{id}/signals 200 | transport/http/fiber:TestDeliverSignal_Fiber | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | stdlib POST /messages 202 | transport/http/stdlib:TestMessageRoutes_NoMatchNoop | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | gin POST /messages 202 | transport/http/gin:TestMessageRoutes_DeliverMessage_202 | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | fiber POST /messages 202 | transport/http/fiber:TestMessageRoutes_Customize | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | stdlib GET /readyz 200 | transport/http/stdlib:TestHealthRoutes_Ready_OK | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | gin GET /readyz 200 | transport/http/gin:TestHealthRoutes_Readyz_200_AllOK | yes |
| Unauthenticated write is the documented default posture | Instance, signal, message and health routes answer an unidentified caller | fiber GET /readyz 200 | transport/http/fiber:TestHealthRoutes_Ready_OK | yes |
| Unauthenticated write is the documented default posture | Admin routes registered without authentication | status 200 with one instance seeded | transport/http/stdlib:TestAdminRoutes_ListInstances_Total_1 | yes |
| Unauthenticated write is the documented default posture | Admin routes registered without authentication | "the body lists the instance" | transport/http/{stdlib,gin,fiber}:TestBaseline_AdminRoutes_UnidentifiedCallerListsInstance | yes (characterization) |
| Unauthenticated write is the documented default posture | Negative control - the human-task routes do refuse the same caller | - | transport/http/stdlib:TestTaskRoutes_NoIdentity401 | yes |
| Admin routes are opt-in and optional admin routes need their dependency | Mount alone registers no admin route | stdlib / gin / fiber | transport/http/stdlib:TestMount_AdminAbsentUntilCustomize; transport/http/gin:TestAdminRoutes_AbsentByDefault; transport/http/fiber:TestMount_AdminAbsentByDefault | yes |
| Admin routes are opt-in and optional admin routes need their dependency | An optional admin route is absent while its dependency is nil | stdlib / gin / fiber | transport/http/stdlib:TestAdminRoutes_ConditionalDep_NilDeadLetters; transport/http/gin:TestAdminRoutes_ConditionalDeadLetters_NilOmitted; transport/http/fiber:TestAdminRoutes_ConditionalDep_NilDeadLetters | yes |
| Admin routes are opt-in and optional admin routes need their dependency | Negative control - an explicitly mounted always-on admin route answers | - | transport/http/stdlib:TestAdminRoutes_ListInstances_Total_1; transport/http/gin:TestAdminRoutes_ListInstances_200 | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | ErrUnauthenticated 401 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/401_unauthenticated_renders_a_static_message; transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/bare_unauthenticated_→_401 (code) | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | ErrIdentityUnavailable 503 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/503_identity_unavailable_sends_no_message_at_all; transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/identity_failure_wrapping_ErrBadInput_→_503,_NOT_400 (code) | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | not found 404 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/404_not_found_renders_the_chain; transport/http/httpcore:TestClassifyError/not_found (code) | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | ErrNotAuthorized 403 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/403_forbidden_renders_the_chain; transport/http/httpcore:TestClassifyError/forbidden (code) | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | concurrent update 409 (status, marker rendered) | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/409_concurrent_update_renders_the_chain | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | concurrent update 409 (code "conflict") | transport/http/httpcore:TestBaseline_ClassifyError_ConcurrentUpdateIsConflict | yes (characterization) |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | ErrRequestBodyTooLarge 413 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/413_request_too_large_renders_a_static_message; transport/http/httpcore:TestOversizedBodyClassifiesAs413NotBadRequest/a_wrapped_oversize_error_leaks_neither_the_wrapper_text_nor_the_limit (code) | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | ErrBadInput 400 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/400_bad_input_renders_the_chain; transport/http/httpcore:TestClassifyError/bad_input_keeps_message (code) | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | service.ErrConflict 422 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/422_conflict_state_renders_the_chain; transport/http/httpcore:TestClassifyErrorOutcomeSentinels/a_duplicate_start_is_a_state_conflict (code) | yes |
| Error responses use one client-safe envelope | Status, code and message rendering per error class | no library sentinel 500 | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/500_default_sends_no_message_at_all; transport/http/httpcore:TestClassifyError/internal_hides_message (code) | yes |
| Error responses use one client-safe envelope | An unclassified failure never reaches the client | stdlib | transport/http/stdlib:TestMount_5xx_NoRawError | yes |
| Error responses use one client-safe envelope | An unclassified failure never reaches the client | gin | transport/http/gin:TestInternalError_NoRawErrorLeak (asserts 500 and an empty message; does not grep the body for the raw text) | yes |
| Error responses use one client-safe envelope | An unclassified failure never reaches the client | fiber | transport/http/fiber:TestMount_5xx_NoRawError | yes |
| Error responses use one client-safe envelope | Error envelopes are identical across adapters | GET missing 404 | transport/http/parity:TestParity_ErrorEnvelopes/404_GET_missing_instance | yes |
| Error responses use one client-safe envelope | Error envelopes are identical across adapters | POST /instances {} 400 | transport/http/parity:TestParity_ErrorEnvelopes/400_empty_JSON_body | yes |
| Identity and body-size errors outrank other classifications | Precedence of co-matching errors | IdentityUnavailable over NotAuthorized | transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/identity_failure_wrapping_ErrNotAuthorized_→_503,_NOT_403 | yes |
| Identity and body-size errors outrank other classifications | Precedence of co-matching errors | IdentityUnavailable over not-found | transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/identity_failure_wrapping_a_not-found_sentinel_→_503,_NOT_404 | yes |
| Identity and body-size errors outrank other classifications | Precedence of co-matching errors | IdentityUnavailable over BadInput | transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/identity_failure_wrapping_ErrBadInput_→_503,_NOT_400 | yes |
| Identity and body-size errors outrank other classifications | Precedence of co-matching errors | IdentityUnavailable around Unauthenticated -> 401 | transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/identity_failure_wrapping_ErrUnauthenticated_→_401,_the_more_specific | yes |
| Identity and body-size errors outrank other classifications | Precedence of co-matching errors | BadInput around TooLarge -> 413 | transport/http/httpcore:TestOversizedBodyClassifiesAs413NotBadRequest/an_error_carrying_BOTH_ErrBadInput_and_the_oversize_sentinel_is_a_413 | yes |
| Identity and body-size errors outrank other classifications | Negative control - plain bad input stays 400 | - | transport/http/httpcore:TestClassifyError/bad_input_keeps_message | yes |
| Inbound request bodies are capped | Cap boundary on a required-body route | unset, 2 MiB -> 413 | transport/http/parity:TestParity_MaxBodyBytes_CoreRoute_413/default_1_MiB_cap_refuses_a_2_MiB_body_on_all_three | yes |
| Inbound request bodies are capped | Cap boundary on a required-body route | 64, 256 -> 413 | transport/http/parity:TestParity_MaxBodyBytes_CoreRoute_413/explicit_64-byte_cap_refuses_a_256-byte_body_on_all_three | yes |
| Inbound request bodies are capped | Cap boundary on a required-body route | 1024, 1025 -> 413 | transport/http/parity:TestParity_MaxBodyBytes_CoreRoute_413/one_byte_over_the_cap_refuses_on_all_three | yes |
| Inbound request bodies are capped | Cap boundary on a required-body route | 1024, 1024 -> 201 | transport/http/parity:TestParity_MaxBodyBytes_CoreRoute_413/a_body_of_exactly_the_cap_is_admitted_on_all_three | yes |
| Inbound request bodies are capped | Cap boundary on a required-body route | 0, 2 MiB -> 201 | transport/http/parity:TestParity_MaxBodyBytes_Disabled/zero_disables_the_cap (open #169 reports this test failing ~1 in 3 under parallel whole-tree load; green in isolation here) | yes |
| Inbound request bodies are capped | Cap boundary on a required-body route | -1, 2 MiB -> 201 | transport/http/parity:TestParity_MaxBodyBytes_Disabled/a_negative_cap_disables_it_too (same #169 note; fiber's TestDisabledCapSkipsBothChecks is the other #169 test) | yes |
| Inbound request bodies are capped | The 413 body is the fixed envelope on every adapter | - | transport/http/parity:TestParity_MaxBodyBytes_CoreRoute_413/explicit_64-byte_cap_refuses_a_256-byte_body_on_all_three | yes |
| Inbound request bodies are capped | The cap also bounds a route whose body is optional | 64, 256-byte -> 413 | transport/http/parity:TestParity_MaxBodyBytes_AdminOptionalBody/oversize_body_is_refused_before_the_optional_decode | yes |
| Inbound request bodies are capped | The cap also bounds a route whose body is optional | 4096, {"add_attempts":1} -> 404 | transport/http/parity:TestParity_MaxBodyBytes_AdminOptionalBody/under-cap_body_still_reaches_the_handler | yes |
| Inbound request bodies are capped | The cap also bounds a route whose body is optional | 4096, no body -> 404 | transport/http/parity:TestParity_MaxBodyBytes_AdminOptionalBody/an_EMPTY_body_still_reaches_the_handler | yes |
| Inbound request bodies are capped | The cap also bounds a route whose body is optional | 0, 2 MiB -> 404 | transport/http/parity:TestParity_MaxBodyBytes_AdminOptionalBody/the_cap_disabled_admits_a_2_MiB_body_on_this_route_too | yes |
| Fiber refuses bodies above its own BodyLimit without an envelope | A body above fiber's BodyLimit | - | transport/http/parity:TestFiberDivergence_AboveFiberConfigBodyLimit | yes |
| Fiber refuses bodies above its own BodyLimit without an envelope | Negative control - below fiber's BodyLimit fiber matches the others | - | transport/http/parity:TestParity_MaxBodyBytes_CoreRoute_413/explicit_64-byte_cap_refuses_a_256-byte_body_on_all_three | yes |
| Responses the library writes carry nosniff | nosniff on each write path | POST /instances 201 | transport/http/parity:TestParity_NosniffHeader/2xx_handler-written_body | yes |
| Responses the library writes carry nosniff | nosniff on each write path | GET missing 404 | transport/http/parity:TestParity_NosniffHeader/4xx_error_envelope | yes |
| Responses the library writes carry nosniff | nosniff on each write path | GET /readyz 200 | transport/http/parity:TestParity_NosniffHeader/health_probe | yes |
| The library does not inject into the consumer's handler chain | Short-circuiting consumer middleware with and without NosniffMiddleware | stdlib/gin/fiber, not installed | transport/http/parity:TestParity_NosniffMiddleware_ShortCircuitingConsumerMiddleware/without_NosniffMiddleware_the_consumer's_own_response_carries_no_header | yes |
| The library does not inject into the consumer's handler chain | Short-circuiting consumer middleware with and without NosniffMiddleware | stdlib/gin/fiber, installed | transport/http/parity:TestParity_NosniffMiddleware_ShortCircuitingConsumerMiddleware/NosniffMiddleware_covers_the_short-circuited_response | yes |
| WithMiddleware runs consumer middleware once per request | Exactly one execution per request on every route group | gin instance / task / message | transport/http/parity:TestParity_WithMiddleware_ScopeAndMultiplicity/gin_runs_it_once_on_an_instance_route, /gin_runs_it_once_on_a_task_route, /gin_runs_it_once_on_a_message_route | yes |
| WithMiddleware runs consumer middleware once per request | Exactly one execution per request on every route group | fiber instance / task / message | transport/http/parity:TestParity_WithMiddleware_ScopeAndMultiplicity/fiber_runs_it_once_on_an_instance_route, /fiber_runs_it_once_on_a_task_route, /fiber_runs_it_once_on_a_message_route | yes |
| WithMiddleware runs consumer middleware once per request | gin leaves the consumer's own routes alone | - | transport/http/parity:TestParity_WithMiddleware_ScopeAndMultiplicity/gin_does_not_run_it_on_the_consumer's_own_route | yes |
| Health probes report readiness without leaking errors | Readiness outcomes | no checks | transport/http/httpcore:TestEvaluateReady/no_checks; transport/http/gin:TestMountHealth_NoChecks | yes |
| Health probes report readiness without leaking errors | Readiness outcomes | every check passing | transport/http/httpcore:TestEvaluateReady/all_checks_pass | yes |
| Health probes report readiness without leaking errors | Readiness outcomes | one check failing | transport/http/httpcore:TestEvaluateReady/one_check_fails (asserts each check value is "ok" or "unavailable") | yes |
| Health probes report readiness without leaking errors | A failing check yields 503 on every adapter | stdlib / gin / fiber | transport/http/stdlib:TestHealthRoutes_Ready_Fail; transport/http/gin:TestHealthRoutes_Readyz_503_CheckFails; transport/http/fiber:TestHealthRoutes_Ready_Fail | yes |
| Health probes report readiness without leaking errors | Liveness answers 200 on every adapter | stdlib / gin / fiber | transport/http/stdlib:TestHealthRoutes_Live; transport/http/gin:TestHealthRoutes_Livez_200; transport/http/fiber:TestHealthRoutes_Live; transport/http/httpcore:TestEvaluateLive (status "ok") | yes |

## human-task-authorization


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Task operations are refused for an actor the task's eligibility does not admit | Ineligible actor is refused on each operation | claim | runtime/task:TestTaskServiceRejectsIneligibleActor | yes |
| Task operations are refused for an actor the task's eligibility does not admit | Ineligible actor is refused on each operation | complete | runtime/task:TestTaskServiceCompleteRejectsUnauthorized | yes |
| Task operations are refused for an actor the task's eligibility does not admit | Ineligible actor is refused on each operation | refresh candidates | runtime/task:TestTaskServiceRefreshCandidates/an_unauthorized_caller_is_rejected; service:TestRefreshTaskCandidates/requester_outside_the_eligibility_spec | yes |
| Task operations are refused for an actor the task's eligibility does not admit | A refused operation changes nothing | claim, complete, refresh candidates | service:TestBaseline_RefusedTaskOperationChangesNothing/{claim,complete,refresh_candidates} | yes (characterization) |
| Task operations are refused for an actor the task's eligibility does not admit | Eligible actor is not refused | claim | runtime/task:TestTaskService_Claim_AttributeOverVars/matching_region_claims; transport/http/httpcore:TestClaimTask_ActorComesFromTheResolverNotTheBody/authenticated_manager_→_200 | yes |
| Task operations are refused for an actor the task's eligibility does not admit | Eligible actor is not refused | complete | transport/http/httpcore:TestCompleteTask/success_→_200_with_completed_status (harness wires authz.NewComposite) | yes |
| Task operations are refused for an actor the task's eligibility does not admit | Eligible actor is not refused | refresh candidates | service:TestRefreshTaskCandidates/refreshed_set_replaces_the_creation-time_candidates (fixture wires authz.NewComposite) | yes |
| Attribute predicates see the task's variable snapshot | Predicate over a task variable decides the claim | "EU" succeeds | runtime/task:TestTaskService_Claim_AttributeOverVars/matching_region_claims; runtime:TestProcessDriverAttributeOverVarsThroughRunner/matching_region_claims | yes |
| Attribute predicates see the task's variable snapshot | Predicate over a task variable decides the claim | "US" ErrNotAuthorized | runtime/task:TestTaskService_Claim_AttributeOverVars/non-matching_region_denied; runtime:TestProcessDriverAttributeOverVarsThroughRunner/non-matching_region_denied | yes |
| Task routes take the actor from the configured identity resolver only | A body-supplied actor cannot promote the caller | stdlib | transport/http/stdlib:TestTaskRoutes_ActorComesFromMiddlewareNotTheBody | yes |
| Task routes take the actor from the configured identity resolver only | A body-supplied actor cannot promote the caller | gin | transport/http/gin:TestTaskRoutes_ActorComesFromMiddlewareNotTheBody | yes |
| Task routes take the actor from the configured identity resolver only | A body-supplied actor cannot promote the caller | fiber | transport/http/fiber:TestTaskRoutes_ActorComesFromMiddlewareNotTheBody | yes |
| Task routes take the actor from the configured identity resolver only | The forged-actor refusal carries the forbidden code | fiber | transport/http/fiber:TestTaskRoutes_ActorComesFromMiddlewareNotTheBody | yes |
| Task routes take the actor from the configured identity resolver only | The forged-actor refusal carries the forbidden code | stdlib, gin | transport/http/{stdlib,gin}:TestBaseline_TaskClaimRefusalCodes/forged_body_actor_is_refused_with_the_forbidden_code | yes (characterization) |
| Task routes take the actor from the configured identity resolver only | An eligible resolved actor claims successfully | stdlib | transport/http/stdlib:TestTaskRoutes_ClaimAcceptsAbsentBody | yes |
| Task routes take the actor from the configured identity resolver only | An eligible resolved actor claims successfully | gin | transport/http/gin:TestTaskRoutes_ClaimBodyIsOptionalButStillBounded/absent_body_claims_the_task | yes |
| Task routes take the actor from the configured identity resolver only | An eligible resolved actor claims successfully | fiber | transport/http/fiber:TestTaskRoutes_ClaimBodyIsOptionalButStillBounded/absent_body | yes |
| Task routes fail closed when no identity can be established | Unauthenticated claim is refused identically on every adapter | stdlib / gin / fiber, no body (status + code) | transport/http/stdlib:TestTaskRoutes_NoIdentity401; transport/http/gin:TestTaskRoutes_NoIdentity401; transport/http/fiber:TestTaskRoutes_NoIdentity401 | yes |
| Task routes fail closed when no identity can be established | Unauthenticated claim is refused identically on every adapter | stdlib / gin / fiber, body naming an eligible actor (status 401, identical envelopes) | transport/http/parity:TestParity_PostTasksClaim_Unauthenticated_401 | yes |
| Task routes fail closed when no identity can be established | Unauthenticated claim is refused identically on every adapter | stdlib / gin / fiber, body naming an eligible actor (code "unauthenticated") | transport/http/{stdlib,gin}:TestBaseline_TaskClaimRefusalCodes/unauthenticated_claim_naming_an_eligible_actor_carries_the_unauthenticated_code; fiber:TestBaseline_TaskClaimUnauthenticatedCode/body_naming_an_eligible_actor | yes (characterization) |
| Task routes fail closed when no identity can be established | Resolver outcomes map to distinct refusals | is not configured | transport/http/httpcore:TestResolveRequestActor/nil_resolver_fails_CLOSED_with_401,_never_a_zero_actor; transport/http/httpcore:TestResolveConfig_RequestActor/default_with_nothing_on_the_context_reports_ErrUnauthenticated; transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/bare_unauthenticated_→_401 | yes |
| Task routes fail closed when no identity can be established | Resolver outcomes map to distinct refusals | returns httpcore.ErrUnauthenticated | transport/http/httpcore:TestResolveRequestActor/resolver_reporting_unauthenticated_passes_through; transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/bare_unauthenticated_→_401 | yes |
| Task routes fail closed when no identity can be established | Resolver outcomes map to distinct refusals | returns the zero actor | transport/http/httpcore:TestResolveRequestActor/the_ZERO_actor_is_refused | yes |
| Task routes fail closed when no identity can be established | Resolver outcomes map to distinct refusals | returns any other error | transport/http/httpcore:TestResolveRequestActor/resolver_error_becomes_ErrIdentityUnavailable_and_keeps_the_cause; transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/503_identity_unavailable_sends_no_message_at_all; transport/http/httpcore:TestClassifyError_IdentitySentinelsOutrankEveryOtherArm/identity_failure_wrapping_ErrBadInput_→_503,_NOT_400 (code) | yes |
| Task routes fail closed when no identity can be established | Resolver outcomes map to distinct refusals | cancelled context | transport/http/httpcore:TestResolveRequestActor/a_cancelled_context_reaches_the_resolver_and_becomes_503 | yes |
| Task routes fail closed when no identity can be established | Resolver outcomes map to distinct refusals | actor with an ID is used | transport/http/httpcore:TestResolveRequestActor/a_whole_actor_arrives_whole,_attributes_included | yes |
| Identity is refused before the request body is read | Body problems do not pre-empt the 401 | stdlib claim/complete/reassign | transport/http/stdlib:TestUnauthenticatedBadJSONIs401NotThe400/claim, /complete, /reassign | yes |
| Identity is refused before the request body is read | Body problems do not pre-empt the 401 | gin claim/complete/reassign | transport/http/gin:TestUnauthenticatedBadJSONIs401NotThe400/claim, /complete, /reassign | yes |
| Identity is refused before the request body is read | Body problems do not pre-empt the 401 | fiber claim/complete/reassign | transport/http/fiber:TestUnauthenticatedOversizeBodyIs401NotThe413 (one loop over the three paths) | yes |
| Identity is refused before the request body is read | The pre-empting 401 carries the unauthenticated code | stdlib | transport/http/stdlib:TestUnauthenticatedBadJSONIs401NotThe400/claim | yes |
| Identity is refused before the request body is read | The pre-empting 401 carries the unauthenticated code | gin, fiber | transport/http/gin:TestBaseline_TaskClaimRefusalCodes/unauthenticated_claim_with_a_JSON_string_body_carries_the_unauthenticated_code; fiber:TestBaseline_TaskClaimUnauthenticatedCode/body_larger_than_the_configured_cap | yes (characterization) |
| Identity is refused before the request body is read | Negative control - an authenticated oversize body is still 413 | - | transport/http/stdlib:TestTaskRoutes_ClaimOversizeBodyStill413 | yes |
| Privilege eligibility matches by exact string | Privilege matching | ["finance-task claim"] allowed | authz:TestRoleAuthorizer_PrivilegesOnlySpec/ACCEPT:_actor_holds_exactly_the_required_privilege (RoleAuthorizer delegates to NewComposite) | yes |
| Privilege eligibility matches by exact string | Privilege matching | ["other", "finance-task claim"] allowed | authz:TestRoleAuthorizer_PrivilegesOnlySpec/ACCEPT:_any-of,_actor_holds_one_of_several_privileges | yes |
| Privilege eligibility matches by exact string | Privilege matching | [] refused | authz:TestRoleAuthorizer_PrivilegesOnlySpec/REFUSE:_actor_holds_none_of_the_required_privileges | yes |
| Privilege eligibility matches by exact string | Privilege matching | ["finance-task *"] refused | authz:TestPrivilegeDecider/matching_is_exact:_no_wildcard_grammar (decider level; the composite's identity stage returns ErrNotAuthorized on Deny) | yes |
| Privilege eligibility matches by exact string | Privilege matching | role "finance-task claim" refused | authz:TestRoleAuthorizer_PrivilegesOnlySpec/REFUSE:_roles_do_not_satisfy_a_privileges-only_spec | yes |
| Privilege eligibility matches by exact string | No prefix hierarchy | - | authz:TestRoleAuthorizer_PrivilegesOnlySpec/REFUSE:_matching_is_exact,_not_a_grammar; authz:TestPrivilegeDecider/matching_is_exact:_no_prefix_hierarchy | yes |
| An empty eligibility spec admits any actor | Empty spec allows | - | authz:TestCompositeCombiner/an_empty_spec_allows | yes |
| An empty eligibility spec admits any actor | A non-empty spec is still evaluated | - | authz:TestRoleAuthorizer_PrivilegesOnlySpec/REFUSE:_actor_holds_none_of_the_required_privileges | yes |
| The attribute predicate constrains every identity decision | Identity and constraint combine | actor.ID == "someone-else" | authz:TestCompositeCombiner/identity_allows_but_a_constraint_denies:_refused | yes |
| The attribute predicate constrains every identity decision | Identity and constraint combine | vars["amount"] < 1000 | authz:TestCompositeCombiner/privilege_identity_plus_a_satisfied_constraint_allows | yes |
| A denial carries no policy detail | Refusal is the bare sentinel | - | authz:TestCompositeCombiner/privileges-only:_no_matching_privilege_refuses (the bare-equality check runs on every refusing row) | yes |
| A denial carries no policy detail | An allowed request returns no error | - | authz:TestCompositeCombiner/privileges-only:_matching_privilege_allows | yes |
| A predicate that cannot be evaluated is a failure, not a denial | Unevaluable predicates fail closed as errors | actor.ID ===== (refused, not ErrNotAuthorized) | authz:TestCompositeCombiner/an_unevaluable_predicate_refuses_without_claiming_a_denial; authz:TestRoleAuthorizer_PredicateFailureIsNotADenial/predicate_that_cannot_compile_is_not_a_denial_either | yes |
| A predicate that cannot be evaluated is a failure, not a denial | Unevaluable predicates fail closed as errors | non-boolean (refused, not ErrNotAuthorized) | authz:TestRoleAuthorizer_PredicateFailureIsNotADenial/predicate_that_cannot_evaluate_is_not_a_denial_and_does_not_leak_its_source; authz:TestAttributeDecider/a_predicate_that_is_not_a_bool_errors_and_is_not_a_denial | yes |
| A predicate that cannot be evaluated is a failure, not a denial | Unevaluable predicates fail closed as errors | both rows: "error naming the attribute predicate" | authz:TestBaseline_UnevaluablePredicateErrorNamesTheAttributePredicate | yes (characterization) |
| A predicate that cannot be evaluated is a failure, not a denial | The HTTP response hides the predicate | - | transport/http/httpcore:TestClassifyError_ChainRenderingPerArm/an_unevaluable_authz_predicate_classifies_500,_not_403 (uses a fabricated error string, not one produced by the authorizer) | yes |
| A predicate that cannot be evaluated is a failure, not a denial | A predicate evaluating false is still a denial | - | authz:TestRoleAuthorizer_PredicateFailureIsNotADenial/predicate_evaluating_false_is_a_denial | yes |
| A spec field no decider evaluates is refused as misconfiguration | Coverage is checked before deciding | attribute only / privileges | authz:TestCompositeRefusesASpecItCannotEvaluate/REFUSE:_privileges_set,_no_decider_reads_them | yes |
| A spec field no decider evaluates is refused as misconfiguration | Coverage is checked before deciding | privilege only / privileges | authz:TestCompositeRefusesASpecItCannotEvaluate/ACCEPT:_privileges_set,_a_decider_reads_them | yes |
| A spec field no decider evaluates is refused as misconfiguration | Coverage is checked before deciding | privilege+role / roles and attribute | authz:TestCompositeRefusesASpecItCannotEvaluate/REFUSE:_attribute_set,_no_decider_reads_it | yes |
| A spec field no decider evaluates is refused as misconfiguration | Coverage is checked before deciding | default composite / roles and attribute | authz:TestCompositeRefusesASpecItCannotEvaluate/ACCEPT:_attribute_set,_a_decider_reads_it | yes |
| A spec field no decider evaluates is refused as misconfiguration | Coverage is checked before deciding | privilege only / roles | authz:TestCompositeRefusesASpecItCannotEvaluate/REFUSE:_roles_set,_no_decider_reads_them | yes |
| A spec field no decider evaluates is refused as misconfiguration | Coverage is checked before deciding | privilege+role / roles | authz:TestCompositeRefusesASpecItCannotEvaluate/ACCEPT:_roles_set,_a_decider_reads_them | yes |
| A spec field no decider evaluates is refused as misconfiguration | Coverage is checked before deciding | privilege only / nothing | authz:TestCompositeRefusesASpecItCannotEvaluate/ACCEPT:_an_empty_spec_needs_no_coverage_at_all | yes |
| A spec field no decider evaluates is refused as misconfiguration | The default composite covers every field | - | authz:TestCompositeRefusesASpecItCannotEvaluate/ACCEPT:_the_default_composite_covers_every_field_at_once | yes |
| An empty or broken composite fails closed as misconfiguration | Misconfigured composites | zero composite | authz:TestCompositeZeroValueFailsClosed/the_zero_Composite_refuses | yes |
| An empty or broken composite fails closed as misconfiguration | Misconfigured composites | nil decider, roles ["admin"] | authz:TestCompositeRejectsMisconfiguration/a_nil_decider_element_is_a_configuration_error,_not_a_panic | yes |
| An empty or broken composite fails closed as misconfiguration | Misconfigured composites | nil decider, nothing | authz:TestCompositeRejectsMisconfiguration/a_nil_decider_is_caught_even_when_the_spec_sets_nothing | yes |
| An empty or broken composite fails closed as misconfiguration | A constraint-only composite is legitimate for a constraint-only spec | - | authz:TestCompositeZeroValueFailsClosed/constraint-only_is_legitimate_when_the_spec_only_needs_a_constraint | yes |
| A user task may not declare both eligible roles and eligible privileges | Identity fields on a user task | both | definition/model:TestValidateRejectsRolesAndPrivilegesTogether/both_eligible_roles_and_eligible_privileges_is_rejected | yes |
| A user task may not declare both eligible roles and eligible privileges | Identity fields on a user task | roles only | definition/model:TestValidateRejectsRolesAndPrivilegesTogether/eligible_roles_alone_is_accepted | yes |
| A user task may not declare both eligible roles and eligible privileges | Identity fields on a user task | privileges only | definition/model:TestValidateRejectsRolesAndPrivilegesTogether/eligible_privileges_alone_is_accepted | yes |
| A user task may not declare both eligible roles and eligible privileges | Identity fields on a user task | privileges + predicate | definition/model:TestValidateRejectsRolesAndPrivilegesTogether/an_attribute_predicate_alongside_either_one_is_accepted | yes |
| A user task may not declare both eligible roles and eligible privileges | Identity fields on a user task | none | definition/model:TestValidateRejectsRolesAndPrivilegesTogether/neither_is_accepted | yes |

## human-task-lifecycle


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`. SQL rows ran against testcontainers Postgres and MySQL plus SQLite (Docker available).

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Claiming records the claimant and moves the task to claimed | Claim on an open task | state claimed, claim names actor and time | engine:TestHumanTaskAuditRecording/claim_records_the_full_actor_and_the_trigger_time; engine:TestHumanClaimedUpdatesTask | yes |
| Claiming records the claimant and moves the task to claimed | Claim on an open task | token stays waiting | engine:TestHumanClaimedUpdatesTask | yes |
| Human-task triggers on a closed task are refused as too late | Closed task on a running instance | HumanClaimed (ErrTaskNotOpen, ErrInvalidTransition, not ErrInstanceTerminal) | engine:TestHumanTaskTriggersOnClosedTaskError/HumanClaimed_on_a_closed_task_errors | yes |
| Human-task triggers on a closed task are refused as too late | Closed task on a running instance | HumanReassigned | engine:TestHumanTaskTriggersOnClosedTaskError/HumanReassigned_on_a_closed_task_errors | yes |
| Human-task triggers on a closed task are refused as too late | Closed task on a running instance | HumanCompleted | engine:TestHumanTaskTriggersOnClosedTaskError/HumanCompleted_on_a_closed_task_errors (ErrInvalidTransition follows from ErrTaskNotOpen's definition; not asserted on this row) | yes |
| Human-task triggers on a closed task are refused as too late | Closed task on a running instance | all rows: "the caller's instance state is not mutated" | engine:TestHumanTaskTriggersOnClosedTaskError (tightened: whole-state snapshot comparison) | yes (characterization) |
| Human-task triggers on a closed task are refused as too late | The same triggers on an open task proceed | HumanClaimed | engine:TestHumanTaskTriggersOnClosedTaskError/HumanClaimed_on_an_OPEN_task_on_a_running_instance_still_succeeds | yes |
| Human-task triggers on a closed task are refused as too late | The same triggers on an open task proceed | HumanReassigned | engine:TestHumanReassignedUpdatesTask | yes |
| Human-task triggers on a closed task are refused as too late | The same triggers on an open task proceed | HumanCompleted | engine:TestHumanCompletedAdvancesAndAudits | yes |
| Human-task triggers on a terminal instance are refused | Terminal instance | failed / HumanClaimed | engine:TestHumanClaimedRejectedOnTerminalInstance; engine:TestTerminalDispatchOutcomes/engine.HumanClaimed | yes |
| Human-task triggers on a terminal instance are refused | Terminal instance | failed / HumanReassigned | engine:TestHumanReassignedRejectedOnTerminalInstance; engine:TestTerminalDispatchOutcomes/engine.HumanReassigned | yes |
| Human-task triggers on a terminal instance are refused | Terminal instance | failed / HumanCompleted | engine:TestHumanCompletedRejectedOnTerminalInstance; engine:TestTerminalDispatchOutcomes/engine.HumanCompleted | yes |
| Human-task triggers on a terminal instance are refused | Terminal instance | terminated / HumanClaimed | engine:TestHumanTaskTriggersOnClosedTaskError/HumanClaimed_on_a_terminated_instance_reports_the_instance,_not_the_task_it_swept_closed | yes |
| Human-task triggers on a terminal instance are refused | Terminal instance | completed / HumanClaimed | engine:TestBaseline_HumanClaimedOnCompletedInstance | yes (characterization) |
| Completion validates the outcome against the declared set | Outcome validation | declared, "approve" | engine:TestHumanCompletedOutcomeValidation/an_outcome_inside_the_declared_set_is_accepted | yes |
| Completion validates the outcome against the declared set | Outcome validation | declared, "maybe" | engine:TestHumanCompletedOutcomeValidation/an_outcome_outside_the_declared_set_is_rejected | yes |
| Completion validates the outcome against the declared set | Outcome validation | declared, "" | engine:TestHumanCompletedOutcomeValidation/an_empty_outcome_is_rejected_when_the_node_declares_outcomes | yes |
| Completion validates the outcome against the declared set | Outcome validation | none declared, "anything" | engine:TestHumanCompletedOutcomeValidation/a_node_declaring_no_outcomes_accepts_any_outcome | yes |
| Completion validates the outcome against the declared set | Outcome validation | none declared, "" | engine:TestHumanTaskAuditRecording/completion_without_an_outcome_records_an_empty_outcome_and_note | yes |
| Completion validates the outcome against the declared set | Outcome sentinels map to 400 | ErrInvalidOutcome | transport/http/httpcore:TestClassifyErrorOutcomeSentinels/an_outcome_outside_the_declared_set_is_a_bad_request | yes |
| Completion validates the outcome against the declared set | Outcome sentinels map to 400 | ErrOutcomeRequired | transport/http/httpcore:TestClassifyErrorOutcomeSentinels/a_missing_required_outcome_is_a_bad_request | yes |
| A wait-mode manual task completes only on a bare trigger | Manual task payloads | nothing | engine:TestHumanCompletedOutcomeValidation/a_bare_manual_completion_is_accepted | yes |
| A wait-mode manual task completes only on a bare trigger | Manual task payloads | an outcome | engine:TestHumanCompletedOutcomeValidation/a_wait-mode_manual_task_rejects_an_outcome | yes |
| A wait-mode manual task completes only on a bare trigger | Manual task payloads | a note | engine:TestHumanCompletedOutcomeValidation/a_wait-mode_manual_task_rejects_a_note | yes |
| A wait-mode manual task completes only on a bare trigger | Manual task payloads | a non-empty output | engine:TestHumanCompletedOutcomeValidation/a_wait-mode_manual_task_still_rejects_an_output | yes |
| Completion records who completed the task and with what disposition | Completion audit | actor, time, outcome, note on the task | engine:TestHumanTaskAuditRecording/completion_records_actor,_time,_outcome_and_note | yes |
| Completion records who completed the task and with what disposition | Completion audit | state completed, output merged, token advanced | engine:TestHumanCompletedAdvancesAndAudits | yes |
| Reassignment requires a target | Reassignment inputs | claimed task, "" | runtime/task:TestTaskServiceReassignRejectsAnEmptyTarget/an_empty_target_is_refused | yes |
| Reassignment requires a target | Reassignment inputs | unknown task id, "" | runtime/task:TestTaskServiceReassignRejectsAnEmptyTarget/an_empty_target_is_refused_before_the_task_is_even_looked_up | yes |
| Reassignment requires a target | Reassignment inputs | claimed task, "bob" | runtime/task:TestTaskServiceReassignRejectsAnEmptyTarget/a_named_target_still_yields_a_trigger | yes |
| Reassignment requires a target | Empty target refused at the engine too | engine refusal | engine:TestStepRejectsAReassignmentWithNoTarget/an_empty_target_is_refused_before_any_state_is_touched | yes |
| Reassignment requires a target | Empty target refused at the engine too | HTTP 400 | transport/http/httpcore:TestClassifyErrorClaimInvariantSentinels/an_empty_reassignment_target_is_a_bad_request | yes |
| Reassignment replaces the claim rather than appending | Claim is overwritten | - | engine:TestHumanTaskAuditRecording/reassignment_overwrites_the_claim_with_the_new_assignee; engine:TestHumanReassignedUpdatesTask | yes |
| A candidate resolution replaces the list and leaves closed tasks frozen | Candidate resolution | open [a,b] -> [c] | engine:TestHumanCandidatesResolved/a_later_resolution_replaces_the_list_rather_than_merging_it | yes |
| A candidate resolution replaces the list and leaves closed tasks frozen | Candidate resolution | already [c] -> [c] | engine:TestHumanCandidatesResolved/re-applying_the_same_resolution_is_idempotent | yes |
| A candidate resolution replaces the list and leaves closed tasks frozen | Candidate resolution | closed task | engine:TestHumanCandidatesResolved/a_closed_task_is_left_frozen | yes |
| A candidate resolution replaces the list and leaves closed tasks frozen | Candidate resolution | terminal instance | engine:TestHumanCandidatesResolvedIsolation/a_terminal_instance_is_left_untouched | yes |
| Refreshing candidates applies to open tasks only | Refresh outcomes | open + resolver | runtime/task:TestTaskServiceRefreshCandidates/re-resolves_the_eligible_actors_and_returns_the_replacing_trigger | yes |
| Refreshing candidates applies to open tasks only | Refresh outcomes | registry shrank | runtime/task:TestTaskServiceRefreshCandidates/a_shrinking_registry_is_reflected | yes |
| Refreshing candidates applies to open tasks only | Refresh outcomes | no resolver | runtime/task:TestTaskServiceRefreshCandidates/no_resolver_configured_is_rejected | yes |
| Refreshing candidates applies to open tasks only | Refresh outcomes | unknown task id | runtime/task:TestTaskServiceRefreshCandidates/an_unknown_task_is_rejected | yes |
| Refreshing candidates applies to open tasks only | Refresh outcomes | completed task | runtime/task:TestTaskServiceRefreshCandidates/a_closed_task_is_rejected_so_a_completed_audit_is_not_rewritten | yes |
| Refreshing candidates applies to open tasks only | A refresh on a closed task classifies as a state conflict | - | service:TestRefreshTaskCandidatesOnClosedTaskMapsTo422/closed_task_is_refused_with_ErrTaskNotOpen_and_classified_422 | yes |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | memory: claimed w/o claim, unclaimed w/ claim, out of range | humantask:TestMemTaskStoreUpsertRejectsAnInvalidTask/claimed_without_a_claim, /unclaimed_carrying_a_claim, /out-of-range_state | yes |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | memory: unclaimed w/o claim stored | humantask:TestMemTaskStore_UpsertGet_RoundTrip | yes |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | memory: claimed w/ claim stored | humantask:TestMemTaskStore_Upsert_UpdatesExisting | yes |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | memory: completed w/o claim stored; cancelled w/ claim stored | humantask:TestBaseline_MemTaskStoreUpsertAcceptsClosedTasks | yes (characterization) |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | sql postgres/mysql/sqlite: claimed w/o claim | internal/persistence/store:TestHumanTaskStoreConformance/{postgres,mysql,sqlite}/upsert_rejects_an_invalid_task/claimed_without_a_claim_is_rejected | yes |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | sql postgres/mysql/sqlite: unclaimed w/ claim | internal/persistence/store:TestHumanTaskStoreConformance/{postgres,mysql,sqlite}/upsert_rejects_an_invalid_task/unclaimed_carrying_a_claim_is_rejected | yes |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | sql postgres/mysql/sqlite: out of range | internal/persistence/store:TestHumanTaskStoreConformance/{postgres,mysql,sqlite}/upsert_rejects_an_invalid_task/an_out-of-range_state_is_rejected | yes |
| Task stores refuse a task whose claim contradicts its state | Claim invariant on write | sql postgres/mysql/sqlite: claimed w/ claim stored | internal/persistence/store:TestHumanTaskStoreConformance/{postgres,mysql,sqlite}/upsert_rejects_an_invalid_task/control:_a_legally_claimed_task_is_stored_and_listed | yes |
| An empty actor ID lists no assigned tasks | Empty actor ID is not a wildcard | memory | humantask:TestMemTaskStore_AssignedTo/an_empty_actor_ID_is_not_a_wildcard_over_unclaimed_tasks | yes |
| An empty actor ID lists no assigned tasks | Empty actor ID is not a wildcard | sql postgres/mysql/sqlite | internal/persistence/store:TestHumanTaskStoreConformance/{postgres,mysql,sqlite}/assigned_to_rejects_an_empty_actor_id | yes |
| An empty actor ID lists no assigned tasks | A real claimant still sees its tasks | memory | humantask:TestMemTaskStore_AssignedTo/a_claimed_task_matches_its_claimant,_sorted_by_TaskID | yes |
| An empty actor ID lists no assigned tasks | A real claimant still sees its tasks | sql postgres/mysql/sqlite | internal/persistence/store:TestHumanTaskStoreConformance/{postgres,mysql,sqlite}/assigned_to_filters_by_claimed_by_and_sorts (same shape, fixture IDs "bob"/"carol") | yes |

## instance-chaining




| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Chainer.Start returns only once all three terminal topics are live | A terminal event published once after Start chains its successor | all | eventing:TestChainerStartIsReadyBeforeItReturns/Start_returns_only_once_every_topic_is_live,_so_a_single_publish_lands | yes |
| Chainer.Start returns only once all three terminal topics are live | A partial failure strands nothing | instance.completed | eventing:TestChainerStartStrandsNothingOnPartialFailure/the_first_topic_fails:_nothing_was_started,_nothing_to_strand | yes |
| Chainer.Start returns only once all three terminal topics are live | A partial failure strands nothing | instance.failed | eventing:TestChainerStartStrandsNothingOnPartialFailure/the_middle_topic_fails:_the_one_already_live_must_be_stopped | yes |
| Chainer.Start returns only once all three terminal topics are live | A partial failure strands nothing | instance.terminated | eventing:TestChainerStartStrandsNothingOnPartialFailure/the_last_topic_fails:_both_already_live_must_be_stopped | yes |
| Chainer.Start returns only once all three terminal topics are live | stop ends all three subscriptions and is idempotent | all | eventing:TestChainerStartStopIsIdempotentAndJoins | yes |
| Chainer.Start returns only once all three terminal topics are live | stop waits for an in-flight chain handler | all | eventing:TestChainerStartStopJoinsTheDeliveryLoops | yes |
| The chain handler acks or nacks by outcome | Ack discipline per envelope | instance.completed | eventing:TestChainHandlerProjection/completed_topic_projects_OutcomeCompleted_with_vars | yes |
| The chain handler acks or nacks by outcome | Ack discipline per envelope | instance.failed | eventing:TestChainHandlerProjection/failed_topic_projects_OutcomeFailed | yes |
| The chain handler acks or nacks by outcome | Ack discipline per envelope | instance.terminated | eventing:TestChainHandlerProjection/terminated_topic_projects_OutcomeTerminated | yes |
| The chain handler acks or nacks by outcome | Ack discipline per envelope | something.else | eventing:TestChainHandlerProjection/unknown_topic_is_acked_without_chaining | yes |
| The chain handler acks or nacks by outcome | Ack discipline per envelope | {not json | eventing:TestChainHandlerProjection/malformed_payload_is_acked_without_chaining | yes |
| The chain handler acks or nacks by outcome | A transient start failure nacks the envelope | all | eventing:TestChainHandlerProjection/a_transient_start_failure_is_returned_so_the_envelope_is_nacked | yes |
| Chaining is idempotent under at-least-once delivery | Handle outcomes | declines | chain:TestChainerHandle/no_successor_(ok=false)_does_nothing | yes |
| Chaining is idempotent under at-least-once delivery | Handle outcomes | nil definition | chain:TestChainerHandle/no_successor_(nil_Def)_does_nothing | yes |
| Chaining is idempotent under at-least-once delivery | Handle outcomes | decides, succeeds | chain:TestChainerHandle/happy_path_records_link_and_starts_successor_with_deterministic_id | yes |
| Chaining is idempotent under at-least-once delivery | Handle outcomes | starter returns ErrInstanceExists | chain:TestChainerHandle/duplicate_instance_start_is_treated_as_no-op | yes |
| Chaining is idempotent under at-least-once delivery | Handle outcomes | transient failure: result is the starter's error (errors.Is) | chain:TestChainerHandle/transient_start_error_propagates_for_redelivery | yes |
| Chaining is idempotent under at-least-once delivery | Handle outcomes | transient failure: starts = 1 | runtime/chain:TestChainerHandle/transient_start_error_propagates_for_redelivery (tightened) | yes (characterization) |
| Chaining is idempotent under at-least-once delivery | Handle outcomes | no link store | chain:TestChainerHandle/no_link_store_still_starts_(deterministic_id_only) | yes |
| Chaining is idempotent under at-least-once delivery | The successor ID is fixed by predecessor and outcome | a link store | chain:TestChainerHandle/happy_path_records_link_and_starts_successor_with_deterministic_id | yes |
| Chaining is idempotent under at-least-once delivery | The successor ID is fixed by predecessor and outcome | no link store | chain:TestChainerHandle/no_link_store_still_starts_(deterministic_id_only) | yes |
| Chaining is idempotent under at-least-once delivery | A redelivery after a failed start re-attempts the start | all | chain:TestChainerHandleRetriesStartAfterTransientFailure | yes |
| A chaining core needs a starter and a policy | Constructor guards | nil starter, nil policy, both present | chain:TestNewChainerFailsFast/{nil_starter,nil_policy,valid_args}; chain:TestNewChainerNilGuards | yes |

## instance-disclosure


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Unidentified callers receive a structural projection of instance state | No route leaks a planted secret | stdlib, 4 routes | transport/http/stdlib:TestUnauthenticatedReadsDoNotDisclose/plain_read, /snapshot, /actionable, /signal_(same_render_path_as_the_GET) | yes |
| Unidentified callers receive a structural projection of instance state | No route leaks a planted secret | gin, 4 routes | transport/http/gin:TestDisclosureParity_Gin/plain_read, /snapshot, /actionable, /signal | yes |
| Unidentified callers receive a structural projection of instance state | No route leaks a planted secret | fiber, 4 routes | transport/http/fiber:TestDisclosureParity_Fiber/plain_read, /snapshot, /actionable, /signal | yes |
| Unidentified callers receive a structural projection of instance state | Structural fields survive the projection | skeleton kept | runtime/view:TestPublicState_KeepsStructuralFields | yes |
| Unidentified callers receive a structural projection of instance state | Structural fields survive the projection | no variable, actor, note or incident error | runtime/view:TestPublicState_WithholdsEverySecret | yes |
| A caller the resolver does not identify reads the projection | Every unidentified shape is projected | not configured | transport/http/httpcore:TestDisclosingMapper_ProjectsWhenUnidentified/nil_resolver | yes |
| A caller the resolver does not identify reads the projection | Every unidentified shape is projected | returns httpcore.ErrUnauthenticated | transport/http/httpcore:TestDisclosingMapper_ProjectsWhenUnidentified/no_credential | yes |
| A caller the resolver does not identify reads the projection | Every unidentified shape is projected | returns any other error | transport/http/httpcore:TestDisclosingMapper_ProjectsWhenUnidentified/identity_source_failed | yes |
| A caller the resolver does not identify reads the projection | Every unidentified shape is projected | zero actor, nil error | transport/http/httpcore:TestDisclosingMapper_ProjectsWhenUnidentified/zero_actor_returned_with_a_nil_error | yes |
| A caller the resolver does not identify reads the projection | The projection is real, not vacuous | - | transport/http/httpcore:TestDisclosingMapper_HonoursTheDisclosureSet | yes |
| Each disclosure category widens only its own fields | Categories are independent | {actors}: claim actor ID | runtime/view:TestPublicState_CategoriesAreIndependent/actors_alone_restores_the_claim_actor_but_NOT_the_note | yes |
| Each disclosure category widens only its own fields | Categories are independent | {actors}: each candidate actor ID | runtime/view:TestBaseline_PublicState_ActorsRestoresCandidateIDs | yes (characterization) |
| Each disclosure category widens only its own fields | Categories are independent | {actors, notes} | runtime/view:TestPublicState_CategoriesAreIndependent/actors+notes_restores_both | yes |
| Each disclosure category widens only its own fields | Categories are independent | {variables} | runtime/view:TestPublicState_CategoriesAreIndependent/variables_alone_restores_variables_but_NOT_actors | yes |
| Each disclosure category widens only its own fields | Categories are independent | {notes} | runtime/view:TestPublicState_CategoriesAreIndependent/notes_alone_restores_the_note_without_the_actor | yes |
| Each disclosure category widens only its own fields | Categories are independent | {operations} | runtime/view:TestPublicState_CategoriesAreIndependent/operations_restores_the_compensation_cursor_and_incident_ids | yes |
| Each disclosure category widens only its own fields | Categories are independent | {policy} | runtime/view:TestPublicState_CategoriesAreIndependent/policy_alone_restores_eligibility_but_NOT_variables | yes |
| Each disclosure category widens only its own fields | The empty set discloses nothing beyond the skeleton | - | authz:TestZeroDisclosureSet_DisclosesNothing; authz:TestNewDisclosureSet_EmptyCallIsStillClosed | yes |
| The embedded definition is withheld from unidentified callers unless policy is disclosed | Definition embed | {} withheld | transport/http/httpcore:TestWithholdDefinition/unidentified,_closed | yes |
| The embedded definition is withheld from unidentified callers unless policy is disclosed | Definition embed | {policy} included | transport/http/httpcore:TestWithholdDefinition/unidentified,_policy_disclosed | yes |
| Identity is resolved once per request | One resolution per read | 3 routes | transport/http/stdlib:TestResolverCalledOncePerRequest/plain_read, /snapshot, /actionable | yes |
| Projection never alters the source state | Source state is untouched | {}, {actors}, {actors, variables} | runtime/view:TestPublicState_DoesNotMutateInput | yes |

## instance-id-generation




| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Configured generator mints new instance IDs | Service start uses the configured generator | all | service:TestStartInstanceGeneratesID | yes |
| Configured generator mints new instance IDs | Driver start with an empty instance ID uses the configured generator | all | runtime:TestRunGeneratesWhenInstanceIDEmpty | yes |
| An explicit instance ID takes precedence over the generator | Empty versus explicit instance ID | empty -> gen-123 | runtime:TestRunGeneratesWhenInstanceIDEmpty | yes |
| An explicit instance ID takes precedence over the generator | Empty versus explicit instance ID | explicit-1 | runtime:TestRunUsesExplicitInstanceID | yes |
| A generator failure fails the start with the generator's error | Generator error propagates from both entry points | ProcessEngine | service:TestStartInstancePropagatesGeneratorError | yes |
| A generator failure fails the start with the generator's error | Generator error propagates from both entry points | ProcessDriver | runtime:TestRunPropagatesGeneratorError | yes |
| A generator failure fails the start with the generator's error | Negative control, a succeeding generator starts the instance | all | service:TestStartInstanceGeneratesID | yes |
| Shipped generators produce well-formed identifiers | UUIDv7 yields a parseable version-7 UUID | all | idgen:TestUUIDv7 | yes |
| Shipped generators produce well-formed identifiers | XID yields distinct hyphen-free identifiers | first call nil error, first result non-empty and hyphen-free, results differ | idgen:TestXID | yes |
| Shipped generators produce well-formed identifiers | XID yields distinct hyphen-free identifiers | second call nil error; second result non-empty and hyphen-free | runtime/idgen:TestXID (tightened) | yes (characterization) |
| Shipped generators produce well-formed identifiers | Func adapts a plain function | fixed-1 / nil | idgen:TestFunc/returns_wrapped_value | yes |
| Shipped generators produce well-formed identifiers | Func adapts a plain function | error row: error matches boom | idgen:TestFunc/propagates_wrapped_error | yes |
| Shipped generators produce well-formed identifiers | Func adapts a plain function | error row: result is "" | runtime/idgen:TestFunc/propagates_wrapped_error (tightened) | yes (characterization) |

## instance-lifecycle


| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Shutdown drains admitted work before returning | Shutdown waits for an in-flight Drive | single | runtime:TestShutdownDrainsInFlight | yes |
| Shutdown drains admitted work before returning | The drain deadline expires | single | runtime:TestShutdownDrainTimeout | yes |
| A draining driver refuses new external work | Driver entry points after Shutdown | Drive, DeliverMessage, BroadcastSignal, CancelInstance, ResolveIncident, ReverseInstance | runtime:TestExternalEntryPointsRejectedWhenDraining/{6 subtests} | yes |
| A draining driver refuses new external work | Driver entry points after Shutdown | ApplyTrigger | runtime:TestApplyTriggerRejectedWhenDraining | yes |
| A draining driver refuses new external work | Service entry points after Shutdown | StartInstance | service:TestEngineStartInstanceRejectedDuringShutdown | yes |
| A draining driver refuses new external work | Service entry points after Shutdown | ClaimTask, CompleteTask, ReassignTask, RefreshTaskCandidates | service:TestEngineHumanTaskRejectedDuringShutdown/{4 subtests} | yes |
| A draining driver refuses new external work | A timer-start fire while draining | both rows | runtime:TestTimerStartFire/{fires_and_creates_an_instance_when_not_draining,dropped_when_draining} | yes |
| A second start of an instance is refused | Starting an instance twice | real time | engine:TestSecondStartInstanceIsRefused/a_second_start_on_a_live_instance_is_refused | yes |
| A second start of an instance is refused | Starting an instance twice | zero time (ErrInstanceAlreadyStarted) | engine:TestSecondStartInstanceIsRefused/a_second_start_after_a_ZERO-OccurredAt_start_is_refused | yes |
| A second start of an instance is refused | Starting an instance twice | zero time, "matches ErrInvalidTransition" clause | holds by sentinel identity (same value as the real-time row asserts); no separate assertion | yes (via real-time row) |
| A second start of an instance is refused | A fresh instance starts | single | engine:TestSecondStartInstanceIsRefused/CONTROL:_a_genuinely_fresh_instance_still_starts | yes |
| A cancel that will not terminate the instance is reported | Cancel during a partial rollback and during a resuming walk | partial rollback | engine:TestCancelOnAnInFlightWalkIsTruthful/DROPPED_against_an_admin_partial_rollback_reports_not-applicable | yes |
| A cancel that will not terminate the instance is reported | Cancel during a partial rollback and during a resuming walk | resuming walk | engine:TestCancelOnAnInFlightWalkIsTruthful/DEFERRED_behind_a_resuming_walk_still_returns_nil | yes |
| A cancel that will not terminate the instance is reported | The driver keeps the sentinel and still cancels children | parent dropped, child terminated | runtime:TestDroppedCancelStillPropagates/a_parent_whose_OWN_cancel_is_dropped_still_terminates_its_child | yes |
| A cancel that will not terminate the instance is reported | The driver keeps the sentinel and still cancels children | child dropped, grandchild terminated | runtime:TestDroppedCancelStillPropagates/a_child_whose_cancel_is_dropped_does_not_orphan_its_grandchild | yes |
| A cancel that will not terminate the instance is reported | The driver keeps the sentinel and still cancels children | single instance | runtime:TestDroppedCancelSentinelSurvivesTheDriver | yes |
| An ambiguous message start is refused | Two definitions start on the same message | single | runtime:TestDeliverMessageStartBehavior/ambiguous_message-start_across_two_defs_returns_ErrAmbiguousMessageStart | yes |
| An ambiguous message start is refused | A unique message start creates an instance | single | runtime:TestDeliverMessageStartBehavior/starts_a_new_instance_when_no_waiter_and_a_unique_message-start_matches | yes |
| A keyed message start creates one instance per correlation key | Repeated and distinct keys | single | runtime:TestDeliverMessageStartBehavior/keyed_message-start_dedups_by_correlation_key | yes |
| A keyed message start creates one instance per correlation key | Concurrent delivery of one key | single | runtime:TestDeliverMessageConcurrentSameKeyCreatesOne | yes |
| A dead instance and a missing token are told apart | Unknown command and late completion | ActionCompleted unknown command (ErrTokenNotFound) | engine:TestStepActionCompletedUnknownCommandID; ErrInvalidTransition clause engine:TestSentinelWrappingGraph/ErrTokenNotFound_wraps_ErrInvalidTransition | yes |
| A dead instance and a missing token are told apart | Unknown command and late completion | HumanCompleted after cancel | engine:TestCancelRequestedTerminates/user-task-parked | yes |
| A dead instance and a missing token are told apart | Unknown command and late completion | HumanCompleted after event-sub-process completion | engine:TestEventStartSubprocess_Nested_Interrupting | yes |

Every row has an asserting test.

## instance-store


 `store:` tests exercise the store type that `persistence.OpenPostgres/OpenMySQL/OpenSQLite` return.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Instance state round-trips through a versioned store | Create, load and commit on each SQL dialect | Postgres, MySQL, SQLite | store:TestStoreCreateLoadCommit/{postgres,mysql,sqlite} | yes |
| Duplicate creation is refused with ErrInstanceExists | Duplicate and fresh IDs | i1 on Postgres, MySQL, SQLite | store:TestStoreCreateLoadCommit/{postgres,mysql,sqlite} | yes |
| Duplicate creation is refused with ErrInstanceExists | Duplicate and fresh IDs | i9 (fresh ID beside i1 -> version 1) on Postgres, MySQL, SQLite | persistence:TestBaselineInstanceStoreCreatesAFreshIDBesideAnExistingOne/{postgres,mysql,sqlite}/fresh_i9 | yes (characterization) |
| Loading an unknown instance fails with ErrInstanceNotFound | Unknown versus known ID | nope and i1 on Postgres, MySQL, SQLite | store:TestStoreCreateLoadCommit/{postgres,mysql,sqlite}; persistence:TestOpenPostgresNotFoundSentinel (Postgres, via facade sentinel) | yes |
| A commit against a stale version is refused with ErrConcurrentUpdate | Stale token after a successful commit | Postgres, MySQL, SQLite | store:TestStoreCreateLoadCommit/{postgres,mysql,sqlite} | yes |
| Every applied step is recorded in the instance's journal | Journal after create and one commit | Postgres, MySQL, SQLite | store:TestStoreCreateLoadCommit/{postgres,mysql,sqlite} | yes |
| A history cap bounds the inline history without dropping open visits | Capping closed visits | n=1, n=2 through a store configured with a history cap | persistence:TestBaselineInstanceStoreCapsClosedVisits | yes (characterization) |
| Schema migration is idempotent | Second migration is a no-op | Postgres | persistence:TestMigrateIsIdempotent | yes |
| Schema migration is idempotent | Second migration is a no-op | MySQL | persistence:TestMigrateMySQL_Idempotent | yes |
| Schema migration is idempotent | Second migration is a no-op | SQLite | persistence:TestBaselineMigrateSQLiteIsIdempotent | yes (characterization) |

## operator-incidents


| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| A token stranded by a definition defect raises a definition-defect incident | Each stuck shape raises a definition-defect incident | catch-nothing | engine:TestTriggerlessCatchRaisesIncident (subtests: incident naming the catch node; token parked as an incident; instance stays running) | yes |
| A token stranded by a definition defect raises a definition-defect incident | Each stuck shape raises a definition-defect incident | throw-nothing | engine:TestTriggerlessThrowRaisesIncident (same three subtests) | yes |
| A token stranded by a definition defect raises a definition-defect incident | Each stuck shape raises a definition-defect incident | sp: kind, node, TokenIncident, reason, StatusRunning | engine:TestNilSubprocessRaisesIncident | yes |
| A token stranded by a definition defect raises a definition-defect incident | Each stuck shape raises a definition-defect incident | sp: "incident naming that token" clause | engine:TestBaselineStrandedSubprocessIncidentNamesItsToken | yes (characterization) |
| A token stranded by a definition defect raises a definition-defect incident | Each stuck shape raises a definition-defect incident | dead service task, user task, signal throw, send task | engine:TestDeadEndParkRaisesIncident/{service_task...,user_task...,signal_throw...,send_task...} | yes |
| A token stranded by a definition defect raises a definition-defect incident | A stranded catch, throw or sub-process schedules nothing | catch, throw | engine:TestTriggerlessCatchRaisesIncident/the_instance_stays_running_and_schedules_no_work; engine:TestTriggerlessThrowRaisesIncident/the_instance_stays_running_and_schedules_no_work | yes |
| A token stranded by a definition defect raises a definition-defect incident | A stranded catch, throw or sub-process schedules nothing | sub-process | engine:TestNilSubprocessRaisesIncident/no_scope_is_opened_and_no_work_is_scheduled | yes |
| A token stranded by a definition defect raises a definition-defect incident | Well-formed neighbors are not incidents | catch waits on a signal | engine:TestSignalCatchResumesOnSignal | yes |
| A token stranded by a definition defect raises a definition-defect incident | Well-formed neighbors are not incidents | throw declares a signal | engine:TestSignalThrowEmitsCommand | yes |
| A token stranded by a definition defect raises a definition-defect incident | Well-formed neighbors are not incidents | every branch ends in an end event | engine:TestStepActionCompletedReachesEnd | yes |
| An instance holding a definition-defect incident can still be cancelled | Cancelling an instance with a defect incident | catch, throw | engine:TestTriggerlessCatchRaisesIncident/cancellation_still_terminates_the_instance; engine:TestTriggerlessThrowRaisesIncident/cancellation_still_terminates_the_instance | yes |
| An instance holding a definition-defect incident can still be cancelled | Cancelling an instance with a defect incident | signal throw with no outgoing flow | engine:TestDeadEndParkCancellable | yes |
| Only action incidents are resolvable through ResolveIncident | Resolving an action incident re-invokes the action | single | engine:TestStepResolveIncidentReinvokes | yes |
| Only action incidents are resolvable through ResolveIncident | Non-action incidents are refused and kept | IncidentCompensationStall | engine:TestResolveIncidentRefusesACompensationStall | yes |
| Only action incidents are resolvable through ResolveIncident | Non-action incidents are refused and kept | IncidentCompensationFailed | engine:TestCompensationFailedIncidentIsNotResolvable | yes |
| Only action incidents are resolvable through ResolveIncident | Non-action incidents are refused and kept | IncidentDefinitionDefect | engine:TestBaselineResolveIncidentRefusesADefinitionDefect | yes (characterization) |
| Only action incidents are resolvable through ResolveIncident | A terminal instance answers with the terminal refusal | single | engine:TestResolveIncidentOnATerminalInstanceIsRefusedAsTerminal | yes |
| A boundary that can never be armed is warned about, not raised as an incident | Unarmable boundary hosts warn and still run | send task, sub-process, call activity | engine:TestUnarmedBoundary_LogsWarn/{send_task...,sub-process...,call_activity...}; not-armed also engine:TestBoundaryArmingMatchesValidation/{send_task...,sub-process...,call_activity...} | yes |
| A boundary that can never be armed is warned about, not raised as an incident | An error boundary on a call activity does not warn | single | engine:TestUnarmedBoundary_LogsWarn/an_error_boundary_on_the_same_host_does_not_warn | yes |
| A compensation action that stops reporting back raises a stall incident | The stall window elapses | single | engine:TestStallTimerFireRaisesIncidentAndNothingElse | yes |
| A compensation action that stops reporting back raises a stall incident | A dying walk still reports its stall | single | engine:TestStallIncidentIsRaisedOnADyingWalk | yes |
| A compensation action that stops reporting back raises a stall incident | A zero window disables detection | single | engine:TestZeroWindowDisablesStallDetection | yes |
| A compensation action that fails raises a compensation-failed incident | A failed compensation is visible and the walk advances | single | engine:TestFailedCompensationActionIsVisible (first row) | yes |
| Compensation-walk escape verbs act only on the walk in flight | Escape verbs refuse a missing or mismatched walk | all 7 rows | engine:TestEscapeVerbsRefuseAMismatchedCursor/{7 subtests} | yes |
| Compensation-walk escape verbs act only on the walk in flight | Each verb is accepted on the walk it names | retry | engine:TestRetryRedispatchesUnderAFreshCommandID | yes |
| Compensation-walk escape verbs act only on the walk in flight | Each verb is accepted on the walk it names | skip | engine:TestSkipAdvancesTheWalk | yes |
| Compensation-walk escape verbs act only on the walk in flight | Each verb is accepted on the walk it names | abandon | engine:TestAbandonTerminatesAndRetainsOnlyUndispatchedRecords | yes |
| Abandon is refused on a compensation walk that resumes | Abandon on a scope-wide compensation throw | single | engine:TestAbandonIsRefusedOnAResumingWalk | yes |
| Abandon is refused on a compensation walk that resumes | Skip still drains the resuming walk | single | engine:TestAbandonIsRefusedOnAResumingWalk (control half) | yes |

Rows that had no asserting test are covered by characterization tests (task 2.4).

## outbound-http-calls


All tests run with `GOTOOLCHAIN=go1.26.8` (go1.26.8 darwin/arm64), `-count=1`, package `action/httpcall`.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| The default client refuses cross-host redirects | Redirect destinations under the default client | no redirect | action/httpcall:TestHTTPCall_DefaultRedirectPolicy/no_redirect_is_unaffected | yes |
| The default client refuses cross-host redirects | Redirect destinations under the default client | same hostname, other port | action/httpcall:TestHTTPCall_DefaultRedirectPolicy/same-host_redirect_across_PORTS_is_followed | yes |
| The default client refuses cross-host redirects | Redirect destinations under the default client | different hostname | action/httpcall:TestHTTPCall_DefaultRedirectPolicy/cross-host_redirect_is_refused | yes |
| The default client refuses cross-host redirects | Redirect destinations under the default client | same-host loop (error names the 10-redirect limit, not ErrCrossHostRedirect) | action/httpcall:TestHTTPCall_DefaultRedirectPolicy/a_same-host_redirect_loop_stops_at_the_hop_cap | yes |
| The default client refuses cross-host redirects | The boundary is hostname equality, not Go's subdomain rule | api.internal -> sub.api.internal | action/httpcall:TestBaselineRedirectPolicySubdomainIsCrossHost | yes (characterization) |
| The default client refuses cross-host redirects | The boundary is hostname equality, not Go's subdomain rule | api.internal -> elsewhere.example | action/httpcall:TestDefaultRedirectPolicy_Scheme/the_host_check_still_applies_alongside_the_scheme_check | yes |
| The default client refuses https-to-plaintext redirects | Scheme changes along a redirect chain | https -> http same host | action/httpcall:TestDefaultRedirectPolicy_Scheme/https_to_http_on_one_hostname_is_refused | yes |
| The default client refuses https-to-plaintext redirects | Scheme changes along a redirect chain | https:8443 -> http:8080 | action/httpcall:TestDefaultRedirectPolicy_Scheme/a_downgrade_is_refused_even_when_the_port_also_changes | yes |
| The default client refuses https-to-plaintext redirects | Scheme changes along a redirect chain | http -> https | action/httpcall:TestDefaultRedirectPolicy_Scheme/http_to_https_on_one_hostname_is_allowed | yes |
| The default client refuses https-to-plaintext redirects | Scheme changes along a redirect chain | https -> https | action/httpcall:TestDefaultRedirectPolicy_Scheme/https_to_https_is_allowed | yes |
| The default client refuses https-to-plaintext redirects | Scheme changes along a redirect chain | http -> http | action/httpcall:TestDefaultRedirectPolicy_Scheme/http_to_http_is_allowed | yes |
| The default client refuses https-to-plaintext redirects | Scheme changes along a redirect chain | http -> https -> http | action/httpcall:TestDefaultRedirectPolicy_Scheme/a_chain_that_began_plaintext_may_return_to_plaintext | yes |
| The default client refuses https-to-plaintext redirects | A refused downgrade is not retried | - | action/httpcall:TestDo_RefusedDowngradeIsNonRetryable | yes |
| A supplied client is used exactly as given | A supplied client keeps Go's redirect behavior | - | action/httpcall:TestHTTPCall_DefaultRedirectPolicy/an_injected_client_keeps_Go's_default_and_follows_cross-host | yes |
| A supplied client is used exactly as given | A supplied client keeps its own transport | - | action/httpcall:TestPoolSizingOptions/an_injected_client_keeps_its_own_transport | yes |
| Response status classifies retryability | Retryability by response | 400 | action/httpcall:TestHTTPCall/4xx_returns_a_non-retryable_error | yes |
| Response status classifies retryability | Retryability by response | 429 | action/httpcall:TestHTTPCall/429_returns_a_retryable_error | yes |
| Response status classifies retryability | Retryability by response | 503 | action/httpcall:TestHTTPCall/5xx_returns_a_retryable_error | yes |
| Response status classifies retryability | Retryability by response | 408 | action/httpcall:TestBaselineOutboundHTTPCallDo/a_408_answer_is_a_retryable_error | yes (characterization) |
| Response status classifies retryability | Retryability by response | cannot be reached | action/httpcall:TestBaselineOutboundHTTPCallDo/an_unreachable_target_is_a_retryable_error | yes (characterization) |
| Response status classifies retryability | Negative control - a 2xx is a result, not an error | - | action/httpcall:TestHTTPCall/GET_decodes_JSON_body_and_status_into_output_keys | yes |
| Bodies read into memory are capped | Size cap outcomes | 16, under | action/httpcall:TestHTTPCall_ResponseSizeCap/body_under_explicit_cap_succeeds | yes |
| Bodies read into memory are capped | Size cap outcomes | 16, over | action/httpcall:TestHTTPCall_ResponseSizeCap/body_over_explicit_cap_fails_NonRetryable_with_ErrBodyTooLarge | yes |
| Bodies read into memory are capped | Size cap outcomes | 0, over 16 | action/httpcall:TestHTTPCall_ResponseSizeCap/zero_cap_disables_the_bound | yes |
| Bodies read into memory are capped | Size cap outcomes | default, over 10 MiB | action/httpcall:TestHTTPCall_ResponseSizeCap/default_cap_rejects_a_body_over_10_MiB | yes |
| Bodies read into memory are capped | Size cap outcomes | 16, buffered request body over | action/httpcall:TestHTTPCall_ResponseSizeCap/over-cap_buffered_request_body_fails_NonRetryable_before_send | yes |
| Bodies read into memory are capped | An over-cap request body is never sent | - | action/httpcall:TestBaselineOutboundHTTPCallDo/an_over-cap_buffered_request_body_is_never_sent | yes (characterization) |
| A failing body validator blocks the request | Validator rejects the body | no request reaches the target; not retryable | action/httpcall:TestWithBodyValidator_FailsBlocksSend | yes |
| A failing body validator blocks the request | Validator rejects the body | "error naming the body validation failure" | action/httpcall:TestBaselineOutboundHTTPCallDo/a_rejecting_body_validator_blocks_the_request_and_names_the_failure | yes (characterization) |
| A failing body validator blocks the request | Negative control - a passing validator sends the request | - | action/httpcall:TestWithBodyValidator_PassesAndSendsRequest | yes |
| The default client owns a sized connection pool | Connections opened for rounds of 8 concurrent requests | no pool option | action/httpcall:TestPoolSizingOptions/the_default_keeps_every_connection_of_a_concurrent_round_warm | yes |
| The default client owns a sized connection pool | Connections opened for rounds of 8 concurrent requests | idle cap 2 | action/httpcall:TestPoolSizingOptions/a_per-host_idle_cap_below_the_concurrency_reopens_the_excess | yes |
| The default client owns a sized connection pool | Connections opened for rounds of 8 concurrent requests | idle cap 0 | action/httpcall:TestPoolSizingOptions/a_non-positive_idle_cap_restores_Go's_default | yes |
| The default client owns a sized connection pool | Connections opened for rounds of 8 concurrent requests | idle cap -1 | action/httpcall:TestPoolSizingOptions/a_negative_idle_cap_restores_Go's_default_rather_than_leaking_through | yes |
| The default client owns a sized connection pool | Connections opened for rounds of 8 concurrent requests | total cap 1 | action/httpcall:TestPoolSizingOptions/a_total_cap_of_1_serialises_the_round_onto_one_connection | yes |
| The default client owns a sized connection pool | Construction does not depend on the process-wide default transport | - | action/httpcall:TestNewHTTPCallSurvivesWrappedDefaultTransport | yes |

## process-variables


All named tests ran with `GOTOOLCHAIN=go1.26.8 go test ./engine -run ... -count=1`. TestServiceActionInputReservesEngineStampedKeys is an in-package (engine) test of the input builder.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| An instance keeps an independent copy of its start variables | Start variables survive a later change to the live variables | - | engine:TestStartInstance_CapturesStartVariables | yes |
| Completion results are merged into the instance variables | Completion output becomes instance variables | service task | engine:TestMicroStepEventuallyCompletesLikeMacro (both subtests assert Variables["result"]=="ok") | yes |
| Completion results are merged into the instance variables | Completion output becomes instance variables | user task | engine:TestHumanCompletedAdvancesAndAudits | yes |
| A trigger delivered to a terminal or unmatched target leaves variables unchanged | A dropped payload is not merged | failed + signal | engine:TestSignalReceivedIsNoOpOnTerminalInstance | yes |
| A trigger delivered to a terminal or unmatched target leaves variables unchanged | A dropped payload is not merged | failed + message | engine:TestMessageReceivedIsNoOpOnTerminalInstance | yes |
| A trigger delivered to a terminal or unmatched target leaves variables unchanged | A dropped payload is not merged | completed + signal | engine:TestBaselineDroppedPayloadIsNotMerged (completed, signal; reached naturally and with a surviving token) | yes (characterization) |
| A trigger delivered to a terminal or unmatched target leaves variables unchanged | A dropped payload is not merged | completed + message | engine:TestBaselineDroppedPayloadIsNotMerged (completed, message) | yes (characterization) |
| A trigger delivered to a terminal or unmatched target leaves variables unchanged | A dropped payload is not merged | terminated + signal | engine:TestBaselineDroppedPayloadIsNotMerged (terminated, signal) | yes (characterization) |
| A trigger delivered to a terminal or unmatched target leaves variables unchanged | A dropped payload is not merged | terminated + message | engine:TestBaselineDroppedPayloadIsNotMerged (terminated, message) | yes (characterization) |
| A trigger delivered to a terminal or unmatched target leaves variables unchanged | A dropped payload is not merged | running, signal "no-match" | engine:TestEventGatewayMergeVarsFix; engine:TestSignalMatchingNothingMutatesNothing | yes |
| A primary service action receives the variables plus a stable idempotency key | The key is the same on the first attempt and on a retry | - | engine:TestInvokeActionCarriesStableIdempotencyKey | yes |
| A primary service action receives the variables plus a stable idempotency key | The stamp is added beside the instance variables | customerId/amount | engine:TestServiceActionInputReservesEngineStampedKeys/ACCEPT_at_the_limit:_an_ordinary_process_variable_is_untouched | yes |
| A primary service action receives the variables plus a stable idempotency key | The stamp is added beside the instance variables | caller-chosen _idempotencyKey | engine:TestServiceActionInputReservesEngineStampedKeys/ACCEPT_at_the_limit:_an_exactly-matching_caller__idempotencyKey_is_overwritten,_not_dropped | yes |
| A primary service action receives the variables plus a stable idempotency key | The stamp is added beside the instance variables | no variables | engine:TestServiceActionInputReservesEngineStampedKeys/nil_variables_still_produce_an_input_carrying_the_stamp | yes |
| A primary service action never receives a fold-variant of an engine-stamped name | Fold-variants of reserved names are withheld from the action | "_idempotencykey" | engine:TestServiceActionInputReservesEngineStampedKeys/REFUSE:_a_case-variant_of_the_idempotency_stamp_never_reaches_the_action | yes |
| A primary service action never receives a fold-variant of an engine-stamped name | Fold-variants of reserved names are withheld from the action | "_IDEMPOTENCYKEY", "_IdempotencyKey", "_idempotencyKEY" | engine:TestServiceActionInputReservesEngineStampedKeys/REFUSE:_every_case-variant_spelling,_not_just_the_all-lowercase_one | yes |
| A primary service action never receives a fold-variant of an engine-stamped name | Fold-variants of reserved names are withheld from the action | U+212A, U+017F rows | engine:TestServiceActionInputReservesEngineStampedKeys/REFUSE:_a_NON-ASCII_fold_of_the_stamp,_which_is_not_a_case_variant_at_all | yes |
| A primary service action never receives a fold-variant of an engine-stamped name | Fold-variants of reserved names are withheld from the action | "_errormessage", "_ERRORATTEMPTS" | engine:TestServiceActionInputReservesEngineStampedKeys/REFUSE:_case-variants_of__errorMessage_and__errorAttempts_too | yes |
| A primary service action never receives a fold-variant of an engine-stamped name | Keys that fold onto no reserved name still arrive | "_traceId", "_error" | engine:TestServiceActionInputReservesEngineStampedKeys/ACCEPT_at_the_limit:_an_underscore-prefixed_key_that_is_NOT_a_reserved_variant_binds_normally | yes |
| A primary service action never receives a fold-variant of an engine-stamped name | Keys that fold onto no reserved name still arrive | "customerId" | engine:TestServiceActionInputReservesEngineStampedKeys/ACCEPT_at_the_limit:_an_ordinary_process_variable_is_untouched | yes |
| A primary service action never receives a fold-variant of an engine-stamped name | A lenient typed action sees the engine's stamp, not a caller's variant | - | runtime:TestBaselineLenientTypedActionSeesTheEngineStamp | yes (characterization) |
| Reversing an instance restores its variables | A full reverse resets the variables to the start variables | - | engine:TestReverseToStart_ResumesAtStartWithResetVars | yes |
| Reversing an instance restores its variables | A target reverse restores the target's start-of-visit variables | step1 | engine:TestReverseToNode_RestoresTargetStartOfVisitVariables/reverse_to_step1_restores_step1_start-of-visit_vars | yes |
| Reversing an instance restores its variables | A target reverse restores the target's start-of-visit variables | step3 | engine:TestReverseToNode_RestoresTargetStartOfVisitVariables/reverse_to_step3_early-finish_restores_step3_start-of-visit_vars | yes |
| Reversing an instance restores its variables | An empty start-of-visit snapshot leaves the variables untouched | - | engine:TestBaselineReverseToNodeWithEmptySnapshotKeepsVariables | yes (characterization) |

## restart-recovery




| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Persisted timers are re-armed at their original fire time after a restart | A one-shot timer survives a crash on every SQL dialect | Postgres, MySQL, SQLite | runtime:TestRehydrateTimersDurable/{postgres,mysql,sqlite} | yes |
| Persisted timers are re-armed at their original fire time after a restart | Rehydrated timer resumes the instance on the in-memory stores | all | runtime:TestRehydrateTimersResumesAfterRestart | yes |
| A scheduler with a registered timer job store rehydrates on start | Self-rehydration resumes a crashed instance | all | runtime:TestSelfRehydrateOnStart | yes |
| Timers whose definition cannot be resolved do not block the others | Resolvable timers are returned alongside the sentinel | 0 / 1 | runtime:TestLoadScheduledUnresolvedDefinition_ReturnsSentinel | yes |
| Timers whose definition cannot be resolved do not block the others | Resolvable timers are returned alongside the sentinel | 2 / 1 | runtime:TestLoadScheduledPartialUnresolved_ResolvableJobsReturned | yes |
| Timers whose definition cannot be resolved do not block the others | RehydrateTimers re-arms resolvable timers and reports the rest | all | runtime:TestBaselineRehydrateTimersArmsResolvableAndReportsTheRest | yes (characterization) |
| Timers whose definition cannot be resolved do not block the others | Scheduler start is non-fatal on unresolved definitions | all | runtime:TestSchedulerStart_UnresolvedDefinitions_NonFatal | yes |
| Timers whose definition cannot be resolved do not block the others | Negative control, all definitions resolvable | all | runtime:TestRehydrateTimersDurable/{postgres,mysql,sqlite} | yes |
| A timer row that cannot be armed is skipped without failing the batch | Unarmable rows are dropped, healthy rows survive | every row: healthy row returned; second-row outcome | runtime:TestJobStoreLoadSkipsARowItCannotArm/{4 subtests} | yes |
| A timer row that cannot be armed is skipped without failing the batch | Unarmable rows are dropped, healthy rows survive | every row: healthy row's next run is non-zero | runtime:TestBaselineJobStoreLoadKeepsTheHealthyRowArmable | yes (characterization) |
| A timer row that cannot be armed is skipped without failing the batch | RehydrateTimers returns promptly over an unarmable row | all | runtime:TestRehydrateTimersDoesNotWedgeOnALivelockingRow | yes |

## retry-policy


Tests ran with `GOTOOLCHAIN=go1.26.8 go test ./action ./engine ./runtime ./definition/model -run ... -count=1`. TestEffectiveRetryPolicyPrecedence is an in-package (engine) test.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| An action error is retryable unless marked otherwise | Retry classification by error | nil | action:TestIsRetryable/nil_is_retryable-default_(no_error_to_inspect) | yes |
| An action error is retryable unless marked otherwise | Retry classification by error | errors.New | action:TestIsRetryable/plain_error_is_retryable | yes |
| An action error is retryable unless marked otherwise | Retry classification by error | NonRetryable | action:TestIsRetryable/NonRetryable_marks_not_retryable | yes |
| An action error is retryable unless marked otherwise | Retry classification by error | errors.Join(..., NonRetryable) | action:TestIsRetryable/NonRetryable_wrapped_deeper_still_detected | yes |
| An action error is retryable unless marked otherwise | NonRetryable is transparent to inspection | errors.Is, nil | action:TestNonRetryableUnwraps | yes |
| An action error is retryable unless marked otherwise | NonRetryable is transparent to inspection | Error() text | action:TestNonRetryableErrorMessage | yes |
| An action error is retryable unless marked otherwise | A non-retryable action error reaches the engine as non-retryable | NonRetryable | runtime:TestActionFailedHonoursRetryContract/NonRetryable_becomes_non-retryable | yes |
| An action error is retryable unless marked otherwise | A non-retryable action error reaches the engine as non-retryable | plain | runtime:TestActionFailedHonoursRetryContract/plain_error_stays_retryable | yes |
| The effective retry policy is chosen action first, then node, then runtime default | Precedence across the three tiers | 7/9/3 -> 9 | engine:TestEffectiveRetryPolicyPrecedence/override_beats_node_and_default | yes |
| The effective retry policy is chosen action first, then node, then runtime default | Precedence across the three tiers | 7/none/3 -> 7 | engine:TestEffectiveRetryPolicyPrecedence/node_beats_default_when_no_override | yes |
| The effective retry policy is chosen action first, then node, then runtime default | Precedence across the three tiers | none/none/3 -> 3 | engine:TestEffectiveRetryPolicyPrecedence/default_applies_when_node_bare_and_no_override | yes |
| The effective retry policy is chosen action first, then node, then runtime default | Precedence across the three tiers | none/9/none -> 9 | engine:TestEffectiveRetryPolicyPrecedence/override_applies_even_when_node_bare | yes |
| The effective retry policy is chosen action first, then node, then runtime default | Precedence across the three tiers | none/none/none | engine:TestEffectiveRetryPolicyPrecedence/none_when_nothing_set | yes |
| The effective retry policy is chosen action first, then node, then runtime default | The action tier keeps the node's safety fields | - | engine:TestEffectiveRetryPolicyPrecedence/override_field-merges_node_safety_fields | yes |
| The effective retry policy is chosen action first, then node, then runtime default | Without a node policy the action tier carries no safety fields | - | engine:TestEffectiveRetryPolicyPrecedence/override_alone_when_node_bare_—_no_safety_inheritance | yes |
| The effective retry policy is chosen action first, then node, then runtime default | An action's retry specs override a stricter node policy end to end | - | runtime:TestActionRetryOverridesNodeRetry | yes |
| The effective retry policy is chosen action first, then node, then runtime default | A runtime default enables retry for a node that declares none | - | runtime:TestProcessDriverDefaultPolicyEnablesRetry/with_default_policy_enables_retry | yes |
| A retryable failure with budget left schedules a backoff retry | Retry delay by jitter fraction | not set | engine:TestActionFailedRetryBackoffDefaultsToFullInterval/no_jitter_yields_the_full_backoff_interval | yes |
| A retryable failure with budget left schedules a backoff retry | Retry delay by jitter fraction | 0 | engine:TestActionFailedRetryBackoffDefaultsToFullInterval/a_sampled_fraction_of_exactly_zero_yields_the_full_backoff | yes |
| A retryable failure with budget left schedules a backoff retry | Retry delay by jitter fraction | 0.5 | engine:TestActionFailedRetryBackoffDefaultsToFullInterval/explicit_jitter_scales_the_backoff | yes |
| A retryable failure with budget left schedules a backoff retry | The first retry parks the token and counts the attempt | not set | engine:TestBaselineFirstRetryParksTheToken/jitter_not_set | yes (characterization) |
| A retryable failure with budget left schedules a backoff retry | The first retry parks the token and counts the attempt | 0.5 | engine:TestStepSchedulesRetryWithJitteredBackoff | yes |
| A retryable failure with budget left schedules a backoff retry | The retry timer re-invokes the action | - | engine:TestStepRetryTimerReinvokesAction | yes |
| A retryable failure with budget left schedules a backoff retry | With no policy in effect a failure is not retried | - | engine:TestStepNoPolicyKeepsLegacyBehaviour; runtime:TestProcessDriverDefaultPolicyEnablesRetry/without_default_policy_fails_instance | yes |
| Retrying stops at the first terminal condition | Terminal conditions end retrying | MaxAttempts 3, non-retryable error | runtime:TestTypedActionDecodeFailureIsNonRetryable/fractional_variable_into_an_int_field_is_non-retryable (the non-retryable error is a typed decode failure under a MaxAttempts 3 default; no test drives a plain action.NonRetryable error under a policy) | yes |
| Retrying stops at the first terminal condition | Terminal conditions end retrying | MaxAttempts 1, first attempt | engine:TestStepExhaustion/incident_when_no_catch-flow_and_no_boundary (asserts the terminal incident) | yes |
| Retrying stops at the first terminal condition | Terminal conditions end retrying | NonRetryableErrors ["validation"], "input validation failed" | engine:TestBaselineRetryTerminalConditions/NonRetryableErrors_validation,_error_input_validation_failed_is_terminal | yes (characterization) |
| Retrying stops at the first terminal condition | Terminal conditions end retrying | MaxElapsed 1m, 199s | engine:TestBaselineRetryTerminalConditions/MaxElapsed_1m,_199s_after_the_first_retry_is_terminal | yes (characterization) |
| Retrying stops at the first terminal condition | The legal neighbours of each terminal condition are retried | MaxAttempts 3 (runtime default), plain retryable error | runtime:TestTypedActionDecodeFailureIsNonRetryable/control:_a_plain_action_error_stays_retryable_and_arms_a_retry | yes |
| Retrying stops at the first terminal condition | The legal neighbours of each terminal condition are retried | NonRetryableErrors ["validation"], "timeout" | engine:TestBaselineRetryTerminalConditions/NonRetryableErrors_validation,_error_timeout_is_retried | yes (characterization) |
| Retrying stops at the first terminal condition | The legal neighbours of each terminal condition are retried | MaxElapsed 1m, 29s | engine:TestBaselineRetryTerminalConditions/MaxElapsed_1m,_29s_after_the_first_retry_is_retried | yes (characterization) |
| Retrying stops at the first terminal condition | The legal neighbours of each terminal condition are retried | MaxElapsed 1m, exactly 60s | engine:TestBaselineRetryTerminalConditions/MaxElapsed_1m,_exactly_60s_after_the_first_retry_is_retried | yes (characterization) |
| Retrying stops at the first terminal condition | Substring matching of NonRetryableErrors | "input validation failed", "timeout" | definition/model:TestRetryPolicyIsNonRetryable | yes |
| Retrying stops at the first terminal condition | Substring matching of NonRetryableErrors | [""], "timeout" | definition/model:TestBaselineRetryPolicyEmptySubstringMatchesNothing | yes (characterization) |
| Backoff grows exponentially up to the cap | Backoff by attempt | 1s/0, 1s/1, 1s/2, 1s/10 | definition/model:TestRetryPolicyBackoff/attempt0, /attempt1, /attempt2, /capped | yes |
| Backoff grows exponentially up to the cap | Backoff by attempt | 0/0 | definition/model:TestRetryPolicyBackoffZeroInitial | yes |
| The default retry policy carries Temporal-style values | Default policy values | - | definition/model:TestDefaultRetryPolicy | yes |
| The default retry policy carries Temporal-style values | Normalize fills unset fields and keeps set ones | MaxAttempts 5 -> InitialInterval 1s, BackoffCoef 2.0 | definition/model:TestRetryPolicyNormalizeFillsZeros | yes |
| The default retry policy carries Temporal-style values | Normalize fills unset fields and keeps set ones | MaxAttempts 5 -> MaxInterval 100s | definition/model:TestBaselineRetryPolicyNormalize | yes (characterization) |
| The default retry policy carries Temporal-style values | Normalize fills unset fields and keeps set ones | BackoffCoef 0.99 -> 2.0 | definition/model:TestBaselineRetryPolicyNormalize | yes (characterization) |
| The default retry policy carries Temporal-style values | Normalize fills unset fields and keeps set ones | InitialInterval 5s / BackoffCoef 1.0 / MaxInterval 1m kept | definition/model:TestBaselineRetryPolicyNormalize | yes (characterization) |
| Validate refuses a retry policy outside its documented ranges | Retry policy validation by field values | InitialInterval 1s, BackoffCoef -1 | definition/model:TestValidateRejectsBadRetryPolicy | yes |
| Validate refuses a retry policy outside its documented ranges | Retry policy validation by field values | the other seven rows | definition/model:TestBaselineValidateRetryPolicyRanges | yes (characterization) |
| A retry declaration on an action is not an in-process retry | A wrapped action fails once per direct call | - | action:TestWrappersStandaloneDo/retriable_Do_does_NOT_retry_a_failing_action | yes |

## token-routing-and-joins


| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Exclusive gateway takes one branch or refuses the step | Exclusive gateway routing by condition and default | default flow, amount 150 | engine:TestExclusiveGatewayTakesConditionalBranch | yes |
| Exclusive gateway takes one branch or refuses the step | Exclusive gateway routing by condition and default | default flow, amount 5 | engine:TestExclusiveGatewayTakesDefaultBranch | yes |
| Exclusive gateway takes one branch or refuses the step | Exclusive gateway routing by condition and default | absent, amount 5 | engine:TestExclusiveGatewayNoMatchNoDefaultErrors | yes |
| Parallel fork places a token on every outgoing flow | Both branches of a parallel fork start together | single | engine:TestParallelGatewayForksAllBranches | yes |
| Parallel join consumes one crossing token per incoming flow | Two arrivals over one edge do not satisfy the join | single | engine:TestParallelJoinCountsFlowsNotArrivals/two_tokens_over_one_incoming_flow_do_not_satisfy_the_other_flow | yes |
| Parallel join consumes one crossing token per incoming flow | The join fires on the step that delivers its last unsatisfied flow | single | engine:TestParallelJoinCountsFlowsNotArrivals/join_fires_once_every_incoming_flow_delivered,_leaving_the_surplus_parked | yes |
| Parallel join consumes one crossing token per incoming flow | A plain diamond completes when each flow delivers one token | single | engine:TestParallelJoinCountsFlowsNotArrivals/plain_diamond_still_fires_when_each_incoming_flow_delivers_one_token; engine:TestParallelJoinWaitsForAllBranches | yes |
| Parallel join consumes one crossing token per incoming flow | A looping branch still contributes one token | single | engine:TestParallelJoinCountsFlowsNotArrivals/MEASURED_NEGATIVE:_a_loop-back_branch_never_puts_two_tokens_on_one_edge | yes |
| Parallel join tells incoming flows apart without relying on authored flow IDs | Blank incoming flow IDs on a join | all 4 rows | engine:TestParallelJoinIdentifiesFlowsTheEngineMints/{control,A,B,C} | yes |
| Parallel join tells incoming flows apart without relying on authored flow IDs | Two identical blank edges into one join | single | engine:TestParallelJoinIdentifiesFlowsTheEngineMints/twin_edges:_one_crossed_twice,_its_identical_sibling_never | yes |
| Inclusive fork takes every true branch | Inclusive fork branch selection | a=1,b=1 | engine:TestInclusiveForkTakesAllTrueBranches | yes |
| Inclusive fork takes every true branch | Inclusive fork branch selection | a=1,b=0 | engine:TestInclusiveForkSingleTrueBranch | yes |
| Inclusive fork takes every true branch | Inclusive fork branch selection | a=0,b=0 | engine:TestInclusiveForkFallsBackToDefault | yes |
| Inclusive fork takes every true branch | An unconditional flow suppresses the default | single | engine:TestInclusiveForkUnconditionalFlowSuppressesDefault | yes |
| Inclusive fork takes every true branch | Nothing taken and no default | single | engine:TestInclusiveForkNoMatchNoDefaultErrors | yes |
| Inclusive join waits only for branches that can still reach it | A sibling branch is inside a sub-process | single | engine:TestInclusiveJoinWaitsForSubProcessBranch/join_must_not_fire_while_a_sibling_branch_is_inside_a_sub-process | yes |
| Inclusive join waits only for branches that can still reach it | The join fires once when the sub-process drains | single | engine:TestInclusiveJoinWaitsForSubProcessBranch/join_fires_exactly_once,_consuming_both_branches,_after_the_sub-process_drains | yes |
| Inclusive join waits only for branches that can still reach it | An untaken branch does not hold the join | single | engine:TestInclusiveJoinDoesNotWaitForUntakenBranch | yes |
| Inclusive join waits only for branches that can still reach it | A child scope that cannot reach the join does not hold it | single | engine:TestInclusiveJoinWaitsForSubProcessBranch/join_still_fires_when_the_open_child_scope_cannot_reach_it | yes |
| Boundary events are armed on hosts that wait | A timer boundary is armed on every waiting host kind | serviceTask, businessRuleTask, receiveTask, userTask | engine:TestBoundaryArmingMatchesValidation/{service_task,business-rule_task,receive_task,user_task} | yes |
| Boundary events are armed on hosts that wait | A message boundary is armed on a user task | single | engine:TestBoundaryRoutesToItsOwnOutgoingFlowTarget (precondition `require.Len(r1.State.Boundaries, 1)`) | yes |
| A fired boundary routes to its own outgoing flow | Boundary routing with a decoy flow sharing its ID | all 3 rows | engine:TestBoundaryRoutesToItsOwnOutgoingFlowTarget/{blank...,control:_both...,control:_a_blank-ID...} | yes |
| A boundary whose target names no node fails the step | Unresolvable and blank-but-declared boundary targets | undeclared blank, undeclared "ghost" | engine:TestBoundaryFireFailsLoudlyWhenItsTargetNamesNothing/the_outgoing_flow_dangles_on_{a_blank,a_named}_target | yes |
| A boundary whose target names no node fails the step | Unresolvable and blank-but-declared boundary targets | declared node with ID "" | engine:TestBoundaryFireFailsLoudlyWhenItsTargetNamesNothing/the_outgoing_flow_names_a_node_whose_ID_is_genuinely_blank | yes |
| A force-termination end selects the terminal status by its outcome | Outcome selects the terminal status | "abort", "complete", (omitted), "" | engine:TestForceTerminationOutcomeFromTheWire/{"abort"...,"complete"...,an_omitted...,an_empty...} | yes |
| A force-termination end selects the terminal status by its outcome | Parallel work is swept | abort, complete | engine:TestForceTerminationOutcome/{abort,complete} | yes |

Every row has an asserting test.

## transaction-seam




| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| A step's state and timer writes commit together or not at all | A failed step that arms a timer leaves no trace | SQLite | runtime:TestTimerTxFlowSameTxAtomicity | yes |
| A step's state and timer writes commit together or not at all | A failed step that arms a timer leaves no trace | Postgres | runtime:TestTimerTxFlowSameTxAtomicityPostgres | yes |
| A step's state and timer writes commit together or not at all | A failed first step leaves no instance and nothing that can fire | all | runtime:TestTimerTxFlowCreateRollback | yes |
| A step's state and timer writes commit together or not at all | A failed step that cancels a timer keeps the timer live | all | runtime:TestTimerTxFlowRolledBackCancel | yes |
| A step's state and timer writes commit together or not at all | Negative control, a successful step persists its timer | all | runtime:TestTimerTxFlowActivationOrdering | yes |
| RunInTx commits or rolls back the caller's unit as a whole | Callback outcome decides the unit | tx-commit on Postgres, MySQL, SQLite | store:TestStoreRunInTx/{postgres,mysql,sqlite}/fn_success_commits_the_write | yes |
| RunInTx commits or rolls back the caller's unit as a whole | Callback outcome decides the unit | tx-rollback on Postgres, MySQL, SQLite | store:TestStoreRunInTx/{postgres,mysql,sqlite}/fn_error_rolls_back_the_write | yes |
| The caching instance store never serves a rolled-back write | Failed unit evicts the optimistic cache entry | wrap-tx-rollback | persistence:TestCachingInstanceStoreRunInTxEvictsOnRollback | yes |
| The caching instance store never serves a rolled-back write | Failed unit evicts the optimistic cache entry | wrap-tx-panic | persistence:TestCachingInstanceStoreRunInTxEvictsOnPanic | yes |
| The caching instance store never serves a rolled-back write | Negative control, a successful unit stays readable through the wrapper | all | persistence:TestCachingInstanceStoreRunInTxForwardsCapability/backing_with_TxRunner:_fn_success_commits_through_the_wrapper | yes |
| The caching instance store never serves a rolled-back write | Backing store without the capability still runs the callback | all | persistence:TestCachingInstanceStoreRunInTxForwardsCapability/backing_without_TxRunner_falls_back_to_fn(ctx) | yes |

## typed-action-input


All named tests ran with `GOTOOLCHAIN=go1.26.8 go test ./action ./runtime -run ... -count=1`.

| Requirement | Scenario | Row(s) | Test (pkg:TestName[/subtest]) | Ran green on go1.26.8 |
|---|---|---|---|---|
| Typed actions round-trip their envelope through JSON | A typed action reads its declared fields and returns a map | - | action:TestTypedComposesWithWrap (bare.Do on {ref,count} returns {approved:true}) | yes |
| An input that cannot be decoded fails with ErrDecodeInput and is not retried | Undecodable input values are refused as ErrDecodeInput | count 2.5 | action:TestTypedDo/fractional_number_into_an_int_field_is_a_decode_failure | yes |
| An input that cannot be decoded fails with ErrDecodeInput and is not retried | Undecodable input values are refused as ErrDecodeInput | value JSON cannot encode | action:TestTypedDo/an_input_variable_that_cannot_be_marshalled_is_a_decode_failure | yes |
| An input that cannot be decoded fails with ErrDecodeInput and is not retried | A decodable neighbour is accepted | - | action:TestTypedComposesWithWrap | yes |
| An input that cannot be decoded fails with ErrDecodeInput and is not retried | A decode failure under a retry policy arms no retry, while a plain failure does | decode failure | runtime:TestTypedActionDecodeFailureIsNonRetryable/fractional_variable_into_an_int_field_is_non-retryable | yes |
| An input that cannot be decoded fails with ErrDecodeInput and is not retried | A decode failure under a retry policy arms no retry, while a plain failure does | plain error control | runtime:TestTypedActionDecodeFailureIsNonRetryable/control:_a_plain_action_error_stays_retryable_and_arms_a_retry | yes |
| The consumer function's own error passes through unchanged | A function error keeps its identity and stays retryable | - | action:TestTypedDo/context_reaches_fn_and_its_error_is_passed_through_unclassified | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | struct/struct | action:TestTypedConstruction/struct_Out_is_accepted | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | map Out | action:TestTypedConstruction/map[string]any_Out_is_accepted | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | *struct Out | action:TestTypedConstruction/pointer-to-struct_Out_is_accepted | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | *map Out | action:TestTypedConstruction/pointer-to-map_Out_is_accepted | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | string Out | action:TestTypedConstruction/string_Out_is_rejected | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | int Out | action:TestTypedConstruction/int_Out_is_rejected | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | any Out | action:TestTypedConstruction/any_Out_is_rejected | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | slice Out | action:TestTypedConstruction/slice_Out_is_rejected | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | **struct Out | action:TestTypedConstruction/double-pointer_Out_is_rejected | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | strict, struct In | action:TestTypedConstruction/strict_with_a_struct_In_is_accepted | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | strict, *struct In | action:TestTypedConstruction/strict_with_a_pointer-to-struct_In_is_accepted | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | strict, map In | action:TestTypedConstruction/strict_with_a_map_In_is_rejected | yes |
| Typed refuses an unusable action shape at construction | Construction outcome by type shape | strict, any In | action:TestTypedConstruction/strict_with_an_any_In_is_rejected | yes |
| Typed refuses an unusable action shape at construction | A nil function is refused at construction | - | action:TestTypedConstruction/nil_fn_is_rejected | yes |
| Default decoding ignores keys that match no declared field | Undeclared keys are ignored by default | - | action:TestTypedDo/lenient_default_ignores_unknown_keys_and_the_engine's__idempotencyKey | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "unrelated" | action:TestTypedDo/strict_rejects_an_unknown_field_and_names_it | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "orderid" | action:TestTypedDo/strict_rejects_a_case-variant_of_an_ordinary_camelCase_tag | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "_idempotencykey" | action:TestTypedDo/strict_rejects_a_case-variant_of_the_engine's_idempotency_stamp | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "Nested" (ErrDecodeInput + named) | action:TestTypedDo/strict_rejects_a_case-variant_of_a_flattened_embedded_field | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "Nested" (IsRetryable false) | action:TestBaselineTypedActionInputStrict/refuses_a_case-variant_of_a_flattened_embedded_field_as_not_retryable | yes (characterization) |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "Skipped" (ErrDecodeInput + named) | action:TestTypedDo/strict_rejects_a_field_excluded_by_a_json:"-"_tag | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "Skipped" (IsRetryable false) | action:TestBaselineTypedActionInputStrict/refuses_a_json:"-"_excluded_field_name_as_not_retryable | yes (characterization) |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "secret" (ErrDecodeInput + named) | action:TestTypedDo/strict_rejects_an_unexported_field_name | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "secret" (IsRetryable false) | action:TestBaselineTypedActionInputStrict/refuses_an_unexported_field_name_as_not_retryable | yes (characterization) |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "ſKey" (U+017F) | action:TestBaselineTypedActionInputStrict/refuses_a_U+017F_LONG_S_fold_variant_of_a_declared_name | yes (characterization) |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict refuses keys that are not exact declared names | "KKey" (U+212A) | action:TestBaselineTypedActionInputStrict/refuses_a_U+212A_KELVIN_SIGN_fold_variant_of_a_declared_name | yes (characterization) |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict accepts exact declared names | promoted "ref", "count" | action:TestTypedDo/strict_accepts_a_promoted,_an_untagged_and_a_leading-comma_field | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict accepts exact declared names | flattened "nested", "label" | action:TestTypedDo/strict_accepts_fields_flattened_from_an_embedded_POINTER_struct | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict accepts exact declared names | "ref", "_idempotencyKey" | action:TestTypedDo/strict_accepts_an_In_that_declares_the_engine's__idempotencyKey | yes |
| Strict input rejects any top-level key that is not exactly a declared JSON name | Strict accepts exact declared names | "sKey", "kKey" | action:TestBaselineTypedActionInputStrict/accepts_the_exact_declared_names_sKey_and_kKey | yes (characterization) |
| Strict input on a primary service task requires In to declare the idempotency stamp | Strict without the declared stamp fails the service task by name | - | runtime:TestTypedActionDecodeFailureIsNonRetryable/strict_input_rejects_the_engine's__idempotencyKey_stamp | yes |
| Strict input on a primary service task requires In to declare the idempotency stamp | Strict with the declared stamp receives it | - | action:TestTypedDo/strict_accepts_an_In_that_declares_the_engine's__idempotencyKey | yes |
| Strict input constrains which keys may appear, not which must | An absent envelope decodes to the zero In | nil map | action:TestTypedDo/strict_decode_of_a_nil_input_yields_the_zero_In | yes |
| Strict input constrains which keys may appear, not which must | An absent envelope decodes to the zero In | {} | action:TestBaselineTypedActionInputStrict/an_empty_input_map_decodes_to_the_zero_In | yes (characterization) |
| A typed action composes with Wrap as the innermost bare action | Wrap, resolve and unwrap a typed action | - | action:TestTypedComposesWithWrap | yes |
