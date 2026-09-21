# EKI Fabric Protocol Specifications

このディレクトリは、Architectureで確定したContract semanticsをmachine-readable protocolへ落とす正本です。

Protocolの固定順序は次です。

1. **P0 External Client Plane**
   - `external/openapi.json`
   - HTTPS + JSON + OpenAPI
2. **P1 Worker Plane**
   - `worker/v1/worker.proto`
   - Protobuf IDL + Connect reference transport
   - Worker→Controlは`PostEvents`、Control→Workerはserver-streamingまたはpolling fallback
3. **P2 Signed Authority Decisions**
   - deterministic CBOR + CDDL + COSE_Sign1
4. **Adapter Plane**
   - P1と同じProtobuf系Logical SPIを基礎に、必要Kindから順次追加

## 原則

- External ContractとInternal Execution Contractを混ぜない。
- machine-readable schemaはArchitectureより強いAuthorityを持たない。
- 実装structをそのままpublic schemaの正本にしない。
- unknown security-critical semanticsをsilent ignoreしない。
- bulk Artifact bytesをControl message schemaへ埋め込まない。
- Protocol layerを変更してもTask / Attempt / Lease / ArtifactGrant / Result semanticsを変えない。

Architecture:

- `../architecture/CONTRACT_BOUNDARIES.md`
- `../architecture/CONTROL_DATA_PLANE.md`
- `../architecture/TASK_STATE_MODEL.md`
- `../architecture/EXTENSION_SPI.md`

Protocol ADR:

- `../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`
