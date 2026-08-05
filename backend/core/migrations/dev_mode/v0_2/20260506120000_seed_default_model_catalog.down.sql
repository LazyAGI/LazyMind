-- +migrate Down
-- +migrate Dialect postgres

DELETE FROM "default_models" WHERE "id" IN ('63f8bbe57cb542d5b12ae9698d73b5e1', '33590c1c64db4915a231bc8101147b2b', '96ecfbad967f4da38757e5da560bdec8', 'd7c32e56676f479881cba4a096096fc9', '94311f7b03594cee8181ced62c761c64', 'bd62aa34bf04408b846f315fb1638c2e', '326179288ff049378d03f1b31b034a2c', '333095a8f52f4a63bca04ed712e644ff', '9cca86190b9c4c4299f3684b0525f7e1', '8c423f9b9e2d432caddfc7bae7019a67', '291191006aaa4dbc8b6e1d10f1cbc559', '0d1ef7533d96454a8c26095cde985926', '2026ad0e2e35429ba70f51ebd34bd1d6', 'dce59fb09a4249baa5f8ca1c3b6f1d27', '96f18c6ed1d04f88b90283737f65ad1c', 'feb3e79e2c764f5aa9c533805bd3aa09', '0962c15db7ea444dbdee0490687f2398', 'ed9c108462f841d1a66be9e47a6b1e2a', '698667fcc2414de297a87b99dc42850b', '01f11b9a979148748061f80bd9e16b0f', 'c9775b3bafc94c1bb57b19d26a830b76', '789181e1168e4cd9a1ee044fc03a0ad2', '5764db7061ad4f6fa4c9b24c12bb2d12', '598b7b234c0f48cbba586fe646128ec6', '9df2a31c3d034644a917f8f863f2ee91', '616d86523b164335b0a7cba83879b939', 'ed5f69e9f62848388e43f20acad1596a', '618be35bdd2743d88ddf66a6f7ee3818', '3e2a672d89fa4ffe9ef528c1d9465faa', 'cfc2d701c3b54ed0af27d11c20f8519a', 'eaf3694257734071aed82446ea01ff0c', 'c8dc34542f9c487b9f8cc8392ed8037f', '22427cd64412412283541ceff4d90063', '17f88e05a51149a6a3812738acab0444', '59f623715fa4415793345e4919c16a00', '01dcc0f834dd47ec8123a72ee8eb7b69', '141251e696a84a29a2cda919b0c0ea3a', '9d0055bff97342f980972efcf30edaf5', '2ec9acd2f33a42d68093777c568cfa88', '8577cb2d2f8c4fe3ad41df015511b47d', 'e5c412604b1444df92f24bd04a3b4c7a', '96061759cc0a4a819c3ec327f70d6327', '6558ed680ccd45a59c7dafe2e5a3cfd0', '3586e5c63c924a1cbf74c77ceb07adc6', 'dd272c8407f34b94bd0d9f459532baec', '9f2304a13c014426b50b97f11f20cbb2', '5f835f82370e4fefb7d9a0d8b2f7f9da', 'c44202fbd9a34cbf97540181d2435a11', 'acbd8d5098f244d292fbb8bf165640e7', 'db9376e87e904e3bb1c63893d3306b5c', '393539ec76314e2597724ecf54ccb1d4', '6f7cfbae7350493bb1252f89ab9d6cd0', 'b1598ff5f6a94397a6f65ca369b68ad9', '2cedf624fd974f5ea0bf118df2202e5c', '5f998f01dae048a9a0dd81c20af6912f', '7b971cb9e7374877a323122256dcb221', '9e167f95315f4f39bcecfcab3ba142e1', '4afeef38e9524111a91daf893056e621', 'def8a1794d0947d3aa0179c2d2283fa5', 'd2fb906c1d2d422eb289b0c8c966cc9c', '56c25eeeb4d84026830da9c775ffabfb', '472f2a0cac3f49d7a1f44c802113b1f9', '362c2ac23823460b97921843ec8a582c', 'fb9dd64bad57499cb677d0d7144b880a', '34819c1f6bdf4586b3eb059cc3506919', 'cd0105a92178456b830ccb897cc51a2f', 'f48d505247f241618ef42de630fc8044', '035682fe6aaf48cc82b738ff84323d43', '0eb729fac8a64adda6d109684954a772', '6dab3e05f37d4f4bae2cc5f6e8f678a4', '5a88ca618c6a493287dcf1f4be169683', '6117d953461643bc8480259142af246b', '7608d91184ab4899a6016ebb2f2b516f', '2eda8f174fd84b9ba6e5b39823b86813', 'f4b9df89c76b4c0c88f8f7927927e69f', '7242a5add15c4653b935f6765b8b0ca2', '3637ef0549fd41dc94230b364dfa8f27', '983ffb5350ea4d778b04f58affef0659', '145d2980fd264b499092baed74742ebb', 'd75c1134c1a24bbebf3cb2b89b47a728');
DELETE FROM "default_models" WHERE "id" IN ('b12c59dc2f674362ab8ef6b4da10cca2', '0d31893f5aa34928ba5aabe4231a8bee', 'a0cad0c849e5443e9a78c753f78ba9d4', '48f563dee5e24b6fabe9768a1241e743', 'c3bbd3d67d944baab4fa2b483f6b2dce', '05845f71a4d54eae944dc05e1f8bd6fa', '3f14593b5450483382bbff15e076f0f9', 'aa9145bdca164b779256441b25bf4623', '56416c7a62a8415caf917510ac6123b3', '32ec4e55a02e4e798b55182fde03d659', '8c7daa67c2be45588503644c15dbb431', '36a0b08971c64f7e96d301bff29c5480', '4693f50a5eaf493fbfbe18edce5456b8', '09782e12b2d74a66a54f17960f43bc51', '9a4a9f870fbb4a13b6479b9fa5f126ea', '0c380e98b320438c89ee208b7e1a45dc', 'e9af69b1d86d46828c5d4e7aa855f0a6', '3d03ea834c604160b102e41ffca607b8', '125a3e50b966460c99395bcb9c707f6c', '2d15464ca08344048c171568c60c5944', '683f37babe444ff39fece9d85a3c77b6', '55d33b15511b4966bcf8fe45496f3b26', '1a417e77395c4dd1926e1fa0d8f1d84c', '1f6dfa974aca45b5915aa56c020b9a87', 'fd74c337ac8a4518a1bcedccfa3ddfe4', '45c97575865840b0a31ebd1425617169', 'b7766f7c03a240a7a6eb5d94dc9d74d9', 'e40c246fd02340ea94b577f2f8a2e9b3', '1f7b90daab694e80961c7f5d6ae927b6', 'f28d2f0c69cd4311a426586e1692f71d', 'cd3f7ea440454ceb94a502684ef6ccdd', '266bce11c6d34b0c8c2a3b96dc2cc93e', '43d221d2b9134bad98c82895d851034d', 'b0d0bd95b78e4db8bfc95a072f20bc9b', '1d00fc62a1ad45c98e3b778400d9f0c0', 'cc653dca9b83445f99d6410cc290f4f8', '9ba563a99ae9428c8eef6d2e3843ffbd', '9f9c119788094005a28c6cb45d3a1b65', 'a019c43df9ab4c1daa78da165e1d420e', '2bd4d4c1fa874b58885c7ba7decec0db', 'a7dd7e40e1a84b19bd822de534cc6d95', '6f6d7cc031ab42e8a65b80885fcbf7ce', '9b458247e1c04daba1ff4bb402ef2753', 'b18b7d514c224b3ab7646d41d574d33b', '6a12b0eb3613413fbcc9053344ce0541', '691a89881f5a41be94cb85d3ba623643', '1a6051a463c94439bc6b2cb6bed1fb70', '1b9216f3b2184137aa1b804453d46bb4', '6c0c59028c594b7d921ae737ee7a3b35', 'ce1d8a1975c04142b2b0087dbd79c4eb', '5351b6a1dd6943598b87312d0f90232c', '4dc66a8e423f498db68a128d7b21df0c', '196083eb497b470cb8b967b90e387555', 'f61f60eaa99845d1bbc4c1cdd4d90457', '8c12aaa3305044fda6f43b1477518ee2', '32c8552ce8fc4b24ab87abe338b477b6', '57b9a917f2204d4aa0eeefb9061830ac', 'aa00a0518a9d4bf9afdb5f3cce32c912', 'a143b902321540c18aa95593c5861edf', 'f21fb98b9758451a839c9f5ad9d62d0a', '9c69e35af3554040ad2e04986e2ddec8', 'accbc1ca794d46a4a9a5ee1e53475dd5', '675fbc711dae4b7f80f69beae22fedea', '416c8586897c4f72b071b65ebe2529ca', 'ef3bd1ccbd364e4a8ae430579b535765', 'a7c80b1b544a4ab1beec1379374940c4', '022d899f2c8841f1a055d35ec9aa6d62', 'b71a087327e7435fa7bb1a8aed5287e0', '93fc24cfebb746859c2c169994c41ef5', '4489bd3722b64a1e92fa0b41e53c6ab4', 'ae1e7fe518014f00a824ff619a111612', '2b6d890cdb99472d93ae0c679c092960', 'cb566f0db4b44746a2caf721d8002e0e', '5fdd97cbd8554037b2c93e428b47f904', 'c0bbcd2e31794bf289b7906cb427c0ac', '51d78498aa1543eaa3dfabe34d0f5bcc', 'b32b1f1ce31a4526ba78dc687ed372e4', '66e4d916d8d94614800d21f31e9f476c', 'cb54e2fbc500410d835280802385a2df', '41b18563b7a54becabcf106c106f987d');
DELETE FROM "default_models" WHERE "id" IN ('f479fadae80f4ec6929606f5ef542a9f', '0ae4658ce66c451a8ab9441649e95746', 'b1ebdce5447e49789f9226ae3e70d4b0', 'c7fd2d7ede1b4059b1fa222594421361', 'e4c28de1a0e74537b0d55622b79c8b84', '9a41582a78284e30b29bc15019b745cc', '823fc6e3bd0e4d4ba590d5a80caf37e6', '3aad40783023494184b6786169e7b577', 'ce8a5f445a8c40a592df0dbf92208cc6', '1855b86658974444a961a3be00a7d1cc', 'ab75742661e34f35b898f29be25ed9ea', '502aa1b0c38443cc86641bf0bdb6ae5d', '9cbd07970a234594a6e480698395156a', '175aa913e2054caf841c9d4b6f56e98f', '76fde238b39c4fd7accaf21320205098', '50547e2cbc944ee4addba31d758c748b', 'c858490b3d91437890aac01adeff6b81', '8327f658d7b045f4af62174ec54bceee', 'edf825f111d74144b793afba73da91e4', '1078eefb9c3c46719dda1feda2c245fd', '9144a6726daa47a6b1e0d6643ee2ea36', '76d5b34da4b943228ac89eaae3567ffa', '7e62238041454dd7948d13fcece89b39', 'e2ad22e2172f4f0b8c0bdd48e8dc84d8', '5d065105f4824904aef1fee2d165e4de', 'c65c97668bad4f958a85bcd855743b55', '3391c1f5cdfc4048b7c88931f03995e7', '15ebc504625347eb9989aa2f6849d297', '22fd89b044b841f5b95ae0843a5284cd', '7753133dce0c4ae4b64fc8f0c82a3139', '6e17433342ef48eda83b5a5d2f01329d', '081b7781b76340e4bc587b11d05a75d0', '9d44ac102d204e288bc3b4cbcc4ce115', '8c7f31e34d0c4cb9a73d81eec8b72345', 'f7dffff1814541cfb504895c89b82c7a', '6f23248891544441947d402d574a0830', '01651066a3914a01b299383cfb8ba76f', 'c02d28c19e7c45ae82e8cef49367dc0e', 'ec7858cf9cb44bcd8622574fd0113046', 'b0453c19645e4987a47c93c495a1052d', '519a43a62c3f4e7fb34a0eaa2ce7abda', '33bdc8d9a4de49038a9de9ae30d8f263', '022626323f7347daa07dc5ef1457e8e1', '54a26c0b019f480cad1b97bda6633e68', '348dc1374d804bc4aec9fbded2ec5cd2', 'db0cbef8672949ccaf153891c089ec20', 'e847fd47a3ed4f0a9c5680b05a74966c', 'b70e180e1dc543db8e685a01b4fb6e53', '929abfea334d415fb64cadf52ae4216d', '2fee9df85afa497298fc04ee6c9663a6', '85aa77c21c1e4ccb986c1a9509b33de3', '85a94c85b05b45dea0d0c3994fd8a0b6', 'fd224fa531a242d0ae196b0568e1dc8c', '1620fb658a6548fc9e1025689d6a386f', '4acf78b5d5ba464ab6aeb1725ce705cc', '0c90aca5bda2439bb90460bb7c92dc5e', 'a12e3f21d9f74c7689ed3b8c884b1d49', 'fe06cb8df22342febeb1bf2577311fd4', 'b1cc07bb2a034359a227d465248c846f', '8d4ad6291914439abc8a7ddf47ae8ee9', '7a35abff8586484690bf348794c938b8', '80da86ebb0d04a4e9fb327a51e243fad', '0f571f3b9142412aade87659eafe5124', '0da6b0e7d9454a3298bd2ec3bf7dd6a6', 'fbbd9949ffb5478189501c73ba165b4d', '970a1ffb08fe4225a4431a596792ff8b', 'b679b6bca1ee46f5b5785d4e1eddcb94', 'a9d0362d7f0140cc89bc3fd650ce6d09', 'e2b2188131684dd391cec9113b67bac9', '71ddcc920c71437caa3920d3b70985d7', 'c731207494d7414fb31667a7ec243ad3', 'b3ee51b2aba74f7ebc8fd1937a62cb1f', '991d7206e821408c8c48704f0bd1e3d7', 'caea318b97e641b4b5c76435819024f5', '95ad74de6d6441439c4ac4e2055f5e7e', 'fc6c5dd43d464be9b2a1d6435f589a2c', '8802b2d9b55e4f02848c5075b95802ea', '6a2678b3a04c46169bb8926d340875d8', '30f4b82a45bd42b7ac5a5a29ae40e204', '2f5d2112afdd4d82bde3035ac36a7bcb');
DELETE FROM "default_models" WHERE "id" IN ('c60403bf002c42ecbbf819e2fb607436', '883fd871156f47b49ea24433e2fdc670', 'f26a63d0da8248879f41c428286ae604', '9053d82e3f5449c9910499e82da05ecd', 'd6de46f404c04b6d891c0f5a50f0c2ea', '687772c99f834f4181934fc95937f0a4', 'ce3bed75597f4797878b246e86464f0e', '18563bd76c204227a49196760ee70db3', 'eac3833e699241cb83d0088095b65729', '49ab399ce2ed4f468efac2b0ea9b5649', '406f17e5510d4919856957afcd991256', 'eaa6b87d07ce4f4b977a78a60fe9525d', '34c04be1d0c4463b90dcad622c6645c5', '26ea7d4b56f344f4bf24ee1754b998f7', '03be9f544e724472ba7c4a93455b3923', '5a061aca2d3d4891ba28514ca62fb07e', 'dcbe2afc9f8d4cf281b47f32b96e8930', 'd7aa1d4f5bdf4a61b89077c1769f6195', 'bdf9f349c1314515b42402b0b08c1c7b', '1d21f23a067044e4b75c088ce4220041', '78daf713d00c47c48ef6c170ccf3bc83', '0a80d6ce6ccf46c394abd2e9931af99b', 'c3b38ebbcfe642589e1eb7e2c077f154', 'aeecc5036cc04c50bc9c82f854c87f5b', 'd085a0af2d4a48199e7540a174899ff8', 'e9e6abaf0e2c479483af93c41c6b4aa4', 'a5f54e274bec4e30ac844ce89eba86bb', 'd8467ed47f0d46e0a575f28ec32445fc', '0b58810a684a4727bafc46aac3f7048e', '7bf8d5e171d047c19dd87068130dc65f', 'a7d8a18b04dd48b68e1de198262e20f8', 'b9aee44c98cf42beb2f4cc97b811acf3', '17a47e16caf140f3a06c95e579e7902e', '229bb2e63be74160b1299620fc08f4ab', '5994dd38ad0440bdbdf4722d215d0b59', '972cfd3f287b41d09a3bdd164288f01e', '384d17e473424e3a93cda5cf14379159', 'c1890749b8664d6fb673dbdffea006a3', '862b2fde94e04b8792fdb5b8a41b91a3', 'e8d3fb6324c94d4e9308356297e64b41', '1bf440a3d8e84547b371b5dc7eff0216', 'dae08d6a91a444509fd4a102f6bc0be7', '858c5a25243b4d12b2d3e4fec06e05aa', 'b96d8e1e69fd49d28cf9126bceb53cb3', 'abe7b2a048a047e7a363530649264b20', 'd159576ed91344019039f10994e0366c', '7acb670660ce44609c426f8ece1920de', 'dbaad1e1ad01447e88229a3b288fdd7f', 'b658d85406644669ba6a138d3629e335', '53fabf7699ea4bc6b34adabdf02f0b36', 'a8701e14b62648e5903eec60cd70fdb5', 'ffc337b4b5624ef6848ae1c6bf13ee3e', 'b08ad904e78e44c594d0929c6d7f7250', '5725f259583b4ab3979c4e3644132985', '331e8a9f189c42ae8bb8dff75bf7c72d', '2a924faf1fb748358fc97c62d3860b3b', 'b91e1f43513b47f2aae3031a73cf9c17', '6d508975f66545068bb8002e9b4516bb', 'b0f1c8e10cd24c33a55313990b8f6320', 'd5472c3341f04dffb40e142be5f899dc', '2aa37cbe3b5144c5a30c314d58c108b0', 'e47e105f854c4b0280ef56c182fd3251', '64ca86a03bb145049ea2e6dc354bbe14', 'ed105ad7fa82434dbee191d35785489b', '7c19aee5dabf47bdb34b3154ae489541', 'c9fed87f280243ee9174adde1dece4d7', 'e692eddaeee544b3beb0395d55a60860', 'f8e033c637874ac48045109af3711020', '0b280030fde34acfbad2246858906f29', '253832ddedc8420cacec88b1d681ebbc', 'fc0b7caecce14506acb66d4ac48a12af', '8d410aa31c604a3dbcd19df680b302ac', '83d49b2566b4403ebe165942970405b7', 'b0f92ee0596f4531975f3523fa15c52f', 'd597fcc32fc344b2a93d5664892ba170', 'bfd1bdf1aa9f4e21b817294f315da1eb', 'f3e977c4793742d1b362e24dfe143e15', '5b0b02fb3b6f4fd6bdd426bf33f42fd5', '43820480007646c49c63b5cae5441faa', 'fc1bc63ee308472298eec8774a6a92f6');
DELETE FROM "default_models" WHERE "id" IN ('16760d98201d4cd59d1f67bb244bd361', 'b17796ba24e94fbf9dbe5ecd2bf68ebc', '925fa1b199e3452b985ef88a0e1553fa', '1f4bbc6399804f22815ccb92e25c9ec4', 'f865f5b111fb48a3b7774863780ff5f1', 'd569365c08624b0182506f96d4e883ef', '4a2da305c1ae42cf9ef15d92bbc2429c', '954ec99fad044b9fb205e947b8af86cc', '3dd5cc0d29e3414d9e42752ddea725a1', 'cf7a4d52c97440faae6c806801df567e', 'cac19b61cc8e454db4eb85d3e0f1a5c8', '459fff98287047c7b5e39e9e872eb143', '0cf4118a65d24897839f56d62e1be3ca', '0eeaec86ed584a8abd0d1f1270f9226e', '6ed532e5e56c4ce89cf56bd7a9f1de2f', 'dde066922cc4459caea2971aab90b249', 'a7316d879d7246b4adb2409e80e54a94', '7232b164231c4e42ab0f848ad68a5106', 'fa9245a94f784fccb48ff58102adc0e0', '86432a0c8be142a0bdff87f998230106', '23b8f7e76b5d46edb73360158afb9737', 'c5d3a14958944783b761782182f16aa3', 'ec2453a635304410ac28544a2fd6d3f1', '62195432d8b246778b0afc88dbe895a4');

DELETE FROM "default_model_providers" WHERE "name" IN ('Claude', 'DeepSeek', 'Doubao', 'GLM', 'Kimi', 'Minimax', 'OpenAI', 'Qwen', 'SenseNova', 'SiliconFlow');

-- +migrate Dialect sqlite
PRAGMA defer_foreign_keys = ON;
ALTER TABLE "acl_groups" RENAME TO "__v02_acl_groups";
ALTER TABLE "acl_kbs" RENAME TO "__v02_acl_kbs";
ALTER TABLE "acl_rows" RENAME TO "__v02_acl_rows";
ALTER TABLE "acl_user_groups" RENAME TO "__v02_acl_user_groups";
ALTER TABLE "acl_visibility" RENAME TO "__v02_acl_visibility";
ALTER TABLE "agent_thread_records" RENAME TO "__v02_agent_thread_records";
ALTER TABLE "agent_thread_rounds" RENAME TO "__v02_agent_thread_rounds";
ALTER TABLE "agent_thread_steps" RENAME TO "__v02_agent_thread_steps";
ALTER TABLE "agent_threads" RENAME TO "__v02_agent_threads";
ALTER TABLE "agent_user_active_threads" RENAME TO "__v02_agent_user_active_threads";
ALTER TABLE "async_jobs" RENAME TO "__v02_async_jobs";
ALTER TABLE "automation_groups" RENAME TO "__v02_automation_groups";
ALTER TABLE "chat_histories" RENAME TO "__v02_chat_histories";
ALTER TABLE "conversation_artifacts" RENAME TO "__v02_conversation_artifacts";
ALTER TABLE "conversation_idle_events" RENAME TO "__v02_conversation_idle_events";
ALTER TABLE "conversations" RENAME TO "__v02_conversations";
ALTER TABLE "datasets" RENAME TO "__v02_datasets";
ALTER TABLE "default_datasets" RENAME TO "__v02_default_datasets";
ALTER TABLE "default_model_providers" RENAME TO "__v02_default_model_providers";
ALTER TABLE "default_models" RENAME TO "__v02_default_models";
ALTER TABLE "documents" RENAME TO "__v02_documents";
ALTER TABLE "eval_set_import_previews" RENAME TO "__v02_eval_set_import_previews";
ALTER TABLE "eval_set_items" RENAME TO "__v02_eval_set_items";
ALTER TABLE "eval_set_shards" RENAME TO "__v02_eval_set_shards";
ALTER TABLE "eval_sets" RENAME TO "__v02_eval_sets";
ALTER TABLE "external_database_connections" RENAME TO "__v02_external_database_connections";
ALTER TABLE "local_fs_chat_settings" RENAME TO "__v02_local_fs_chat_settings";
ALTER TABLE "mcp_server_tools" RENAME TO "__v02_mcp_server_tools";
ALTER TABLE "mcp_servers" RENAME TO "__v02_mcp_servers";
ALTER TABLE "memory_review" RENAME TO "__v02_memory_review";
ALTER TABLE "multi_answers_chat_histories" RENAME TO "__v02_multi_answers_chat_histories";
ALTER TABLE "multi_answers_switches" RENAME TO "__v02_multi_answers_switches";
ALTER TABLE "personal_resource_blobs" RENAME TO "__v02_personal_resource_blobs";
ALTER TABLE "personal_resource_drafts" RENAME TO "__v02_personal_resource_drafts";
ALTER TABLE "personal_resource_review_action_batches" RENAME TO "__v02_personal_resource_review_action_batches";
ALTER TABLE "personal_resource_review_action_items" RENAME TO "__v02_personal_resource_review_action_items";
ALTER TABLE "personal_resource_review_sessions" RENAME TO "__v02_personal_resource_review_sessions";
ALTER TABLE "personal_resource_revisions" RENAME TO "__v02_personal_resource_revisions";
ALTER TABLE "personal_resources" RENAME TO "__v02_personal_resources";
ALTER TABLE "plugin_attempt_input_bindings" RENAME TO "__v02_plugin_attempt_input_bindings";
ALTER TABLE "plugin_blobs" RENAME TO "__v02_plugin_blobs";
ALTER TABLE "plugin_drafts" RENAME TO "__v02_plugin_drafts";
ALTER TABLE "plugin_human_artifacts" RENAME TO "__v02_plugin_human_artifacts";
ALTER TABLE "plugin_revision_entries" RENAME TO "__v02_plugin_revision_entries";
ALTER TABLE "plugin_revisions" RENAME TO "__v02_plugin_revisions";
ALTER TABLE "plugin_route_decisions" RENAME TO "__v02_plugin_route_decisions";
ALTER TABLE "plugin_run_outbox" RENAME TO "__v02_plugin_run_outbox";
ALTER TABLE "plugin_session_steps" RENAME TO "__v02_plugin_session_steps";
ALTER TABLE "plugin_sessions" RENAME TO "__v02_plugin_sessions";
ALTER TABLE "plugin_slot_order" RENAME TO "__v02_plugin_slot_order";
ALTER TABLE "plugin_slot_revisions" RENAME TO "__v02_plugin_slot_revisions";
ALTER TABLE "plugin_step_intents" RENAME TO "__v02_plugin_step_intents";
ALTER TABLE "plugin_transition_commands" RENAME TO "__v02_plugin_transition_commands";
ALTER TABLE "plugins" RENAME TO "__v02_plugins";
ALTER TABLE "prompt_categories" RENAME TO "__v02_prompt_categories";
ALTER TABLE "prompt_user_states" RENAME TO "__v02_prompt_user_states";
ALTER TABLE "prompts" RENAME TO "__v02_prompts";
ALTER TABLE "resource_session_snapshots" RENAME TO "__v02_resource_session_snapshots";
ALTER TABLE "resource_update_tasks" RENAME TO "__v02_resource_update_tasks";
ALTER TABLE "schedule_dependencies" RENAME TO "__v02_schedule_dependencies";
ALTER TABLE "skill_blobs" RENAME TO "__v02_skill_blobs";
ALTER TABLE "skill_draft_entries" RENAME TO "__v02_skill_draft_entries";
ALTER TABLE "skill_draft_review_action_batches" RENAME TO "__v02_skill_draft_review_action_batches";
ALTER TABLE "skill_draft_review_action_items" RENAME TO "__v02_skill_draft_review_action_items";
ALTER TABLE "skill_draft_review_sessions" RENAME TO "__v02_skill_draft_review_sessions";
ALTER TABLE "skill_drafts" RENAME TO "__v02_skill_drafts";
ALTER TABLE "skill_market_items" RENAME TO "__v02_skill_market_items";
ALTER TABLE "skill_review_results" RENAME TO "__v02_skill_review_results";
ALTER TABLE "skill_review_scheduler_state" RENAME TO "__v02_skill_review_scheduler_state";
ALTER TABLE "skill_review_stats" RENAME TO "__v02_skill_review_stats";
ALTER TABLE "skill_revision_entries" RENAME TO "__v02_skill_revision_entries";
ALTER TABLE "skill_revisions" RENAME TO "__v02_skill_revisions";
ALTER TABLE "skill_search_indexes" RENAME TO "__v02_skill_search_indexes";
ALTER TABLE "skill_share_items" RENAME TO "__v02_skill_share_items";
ALTER TABLE "skill_share_tasks" RENAME TO "__v02_skill_share_tasks";
ALTER TABLE "skills" RENAME TO "__v02_skills";
ALTER TABLE "sub_agent_artifacts" RENAME TO "__v02_sub_agent_artifacts";
ALTER TABLE "sub_agent_steps" RENAME TO "__v02_sub_agent_steps";
ALTER TABLE "sub_agent_tasks" RENAME TO "__v02_sub_agent_tasks";
ALTER TABLE "task_center_tasks" RENAME TO "__v02_task_center_tasks";
ALTER TABLE "task_run_inputs" RENAME TO "__v02_task_run_inputs";
ALTER TABLE "task_run_outputs" RENAME TO "__v02_task_run_outputs";
ALTER TABLE "tasks" RENAME TO "__v02_tasks";
ALTER TABLE "upload_sessions" RENAME TO "__v02_upload_sessions";
ALTER TABLE "uploaded_files" RENAME TO "__v02_uploaded_files";
ALTER TABLE "user_chat_settings" RENAME TO "__v02_user_chat_settings";
ALTER TABLE "user_disabled_tools" RENAME TO "__v02_user_disabled_tools";
ALTER TABLE "user_model_provider_group_models" RENAME TO "__v02_user_model_provider_group_models";
ALTER TABLE "user_model_provider_groups" RENAME TO "__v02_user_model_provider_groups";
ALTER TABLE "user_model_providers" RENAME TO "__v02_user_model_providers";
ALTER TABLE "user_personalization_settings" RENAME TO "__v02_user_personalization_settings";
ALTER TABLE "user_plugin_settings" RENAME TO "__v02_user_plugin_settings";
ALTER TABLE "user_schedules" RENAME TO "__v02_user_schedules";
ALTER TABLE "user_selected_models" RENAME TO "__v02_user_selected_models";
ALTER TABLE "user_selected_providers" RENAME TO "__v02_user_selected_providers";
ALTER TABLE "user_ui_preferences" RENAME TO "__v02_user_ui_preferences";
ALTER TABLE "word_group_conflicts" RENAME TO "__v02_word_group_conflicts";
ALTER TABLE "words" RENAME TO "__v02_words";
DROP INDEX IF EXISTS "idx_acl_resource";
DROP INDEX IF EXISTS "idx_acl_visibility_resource_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_round_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_task_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_round_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_step_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_task_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_thread_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_thread_request_hash";
DROP INDEX IF EXISTS "idx_agent_thread_steps_stage";
DROP INDEX IF EXISTS "idx_agent_thread_steps_status";
DROP INDEX IF EXISTS "idx_agent_thread_steps_thread_active";
DROP INDEX IF EXISTS "idx_agent_thread_steps_thread_order";
DROP INDEX IF EXISTS "idx_agent_threads_current_task_id";
DROP INDEX IF EXISTS "idx_agent_user_active_threads_status_lease";
DROP INDEX IF EXISTS "idx_agent_user_active_threads_thread_id";
DROP INDEX IF EXISTS "idx_async_jobs_idempotency_key";
DROP INDEX IF EXISTS "idx_async_jobs_lock_until";
DROP INDEX IF EXISTS "idx_async_jobs_resource";
DROP INDEX IF EXISTS "idx_async_jobs_status_next";
DROP INDEX IF EXISTS "idx_async_jobs_type_status";
DROP INDEX IF EXISTS "idx_automation_groups_user_id";
DROP INDEX IF EXISTS "idx_chat_histories_conversation_id";
DROP INDEX IF EXISTS "idx_conversation_artifacts_history_id";
DROP INDEX IF EXISTS "idx_conversation_artifacts_owner_conversation_created";
DROP INDEX IF EXISTS "idx_conversation_idle_events_due";
DROP INDEX IF EXISTS "idx_conversation_idle_events_due_at";
DROP INDEX IF EXISTS "idx_conversation_idle_events_session_id";
DROP INDEX IF EXISTS "idx_conversation_idle_events_session_waiting";
DROP INDEX IF EXISTS "idx_conversation_idle_events_status";
DROP INDEX IF EXISTS "idx_conversation_idle_events_user_id";
DROP INDEX IF EXISTS "idx_datasets_kb_id";
DROP INDEX IF EXISTS "idx_documents_dataset_id";
DROP INDEX IF EXISTS "idx_documents_lazyllm_doc_id";
DROP INDEX IF EXISTS "idx_documents_p_id";
DROP INDEX IF EXISTS "idx_eval_set_import_previews_expires_at";
DROP INDEX IF EXISTS "idx_eval_set_import_previews_status";
DROP INDEX IF EXISTS "idx_eval_set_items_set_created";
DROP INDEX IF EXISTS "idx_eval_set_items_set_source";
DROP INDEX IF EXISTS "idx_eval_set_items_set_type";
DROP INDEX IF EXISTS "idx_eval_set_items_set_updated";
DROP INDEX IF EXISTS "idx_eval_set_shards_status";
DROP INDEX IF EXISTS "idx_eval_sets_group_id";
DROP INDEX IF EXISTS "idx_eval_sets_owner_id";
DROP INDEX IF EXISTS "idx_eval_sets_shard_id";
DROP INDEX IF EXISTS "idx_eval_sets_status";
DROP INDEX IF EXISTS "idx_mcp_tools_server";
DROP INDEX IF EXISTS "idx_multi_answers_chat_histories_conversation_id";
DROP INDEX IF EXISTS "idx_personal_resource_drafts_blob";
DROP INDEX IF EXISTS "idx_personal_resource_review_batches_session_created";
DROP INDEX IF EXISTS "idx_personal_resource_review_items_batch";
DROP INDEX IF EXISTS "idx_personal_resource_review_sessions_resource_status";
DROP INDEX IF EXISTS "idx_personal_resource_revisions_blob";
DROP INDEX IF EXISTS "idx_personal_resource_revisions_created";
DROP INDEX IF EXISTS "idx_plugin_attempt_input_bindings_attempt_id";
DROP INDEX IF EXISTS "idx_plugin_attempt_input_bindings_material_revision_id";
DROP INDEX IF EXISTS "idx_plugin_attempt_input_bindings_session_id";
DROP INDEX IF EXISTS "idx_plugin_drafts_created_by";
DROP INDEX IF EXISTS "idx_plugin_drafts_user_plugin_id";
DROP INDEX IF EXISTS "idx_plugin_revisions_resource";
DROP INDEX IF EXISTS "idx_plugin_route_decisions_session_id";
DROP INDEX IF EXISTS "idx_plugin_run_outbox_status";
DROP INDEX IF EXISTS "idx_plugin_transition_commands_session_id";
DROP INDEX IF EXISTS "idx_plugins_owner";
DROP INDEX IF EXISTS "idx_plugins_plugin_ref";
DROP INDEX IF EXISTS "idx_plugins_relative_root";
DROP INDEX IF EXISTS "idx_resource_session_snapshots_session_id";
DROP INDEX IF EXISTS "idx_resource_uid";
DROP INDEX IF EXISTS "idx_resource_update_tasks_pending";
DROP INDEX IF EXISTS "idx_resource_update_tasks_resource_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_resource_type";
DROP INDEX IF EXISTS "idx_resource_update_tasks_result_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_review_result_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_running_lock";
DROP INDEX IF EXISTS "idx_resource_update_tasks_status";
DROP INDEX IF EXISTS "idx_resource_update_tasks_task_type";
DROP INDEX IF EXISTS "idx_resource_update_tasks_trigger_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_trigger_type";
DROP INDEX IF EXISTS "idx_resource_update_tasks_user_created";
DROP INDEX IF EXISTS "idx_resource_update_tasks_user_id";
DROP INDEX IF EXISTS "idx_schedule_dependencies_source_schedule_id";
DROP INDEX IF EXISTS "idx_schedule_dependencies_target_schedule_id";
DROP INDEX IF EXISTS "idx_schedule_dependencies_user_id";
DROP INDEX IF EXISTS "idx_skill_draft_review_batches_session_created";
DROP INDEX IF EXISTS "idx_skill_draft_review_items_batch";
DROP INDEX IF EXISTS "idx_skill_draft_review_items_session_hunk";
DROP INDEX IF EXISTS "idx_skill_draft_review_sessions_skill_status";
DROP INDEX IF EXISTS "idx_skill_review_scheduler_state_scan";
DROP INDEX IF EXISTS "idx_skill_review_stats_user_request_status";
DROP INDEX IF EXISTS "idx_skill_review_stats_user_status_started";
DROP INDEX IF EXISTS "idx_skill_search_owner";
DROP INDEX IF EXISTS "idx_skill_share_items_source_skill";
DROP INDEX IF EXISTS "idx_skill_share_items_target_user";
DROP INDEX IF EXISTS "idx_skill_share_tasks_source_user";
DROP INDEX IF EXISTS "idx_task_center_tasks_group_id";
DROP INDEX IF EXISTS "idx_task_center_tasks_scheduled_fire_at";
DROP INDEX IF EXISTS "idx_task_run_inputs_downstream_task_id";
DROP INDEX IF EXISTS "idx_task_run_inputs_upstream_task_id";
DROP INDEX IF EXISTS "idx_task_run_outputs_conversation_id";
DROP INDEX IF EXISTS "idx_task_run_outputs_task_id";
DROP INDEX IF EXISTS "idx_tasks_algo_id";
DROP INDEX IF EXISTS "idx_tasks_dataset_id";
DROP INDEX IF EXISTS "idx_tasks_doc_id";
DROP INDEX IF EXISTS "idx_tasks_document_p_id";
DROP INDEX IF EXISTS "idx_tasks_kb_id";
DROP INDEX IF EXISTS "idx_tasks_lazyllm_task_id";
DROP INDEX IF EXISTS "idx_tasks_target_dataset_id";
DROP INDEX IF EXISTS "idx_tasks_task_type";
DROP INDEX IF EXISTS "idx_tct_user_status";
DROP INDEX IF EXISTS "idx_upload_sessions_dataset_id";
DROP INDEX IF EXISTS "idx_upload_sessions_document_id";
DROP INDEX IF EXISTS "idx_upload_sessions_task_id";
DROP INDEX IF EXISTS "idx_upload_sessions_tenant_id";
DROP INDEX IF EXISTS "idx_upload_sessions_upload_id";
DROP INDEX IF EXISTS "idx_upload_sessions_upload_state";
DROP INDEX IF EXISTS "idx_uploaded_files_dataset_id";
DROP INDEX IF EXISTS "idx_uploaded_files_document_id";
DROP INDEX IF EXISTS "idx_uploaded_files_status";
DROP INDEX IF EXISTS "idx_uploaded_files_task_id";
DROP INDEX IF EXISTS "idx_uploaded_files_tenant_id";
DROP INDEX IF EXISTS "idx_uploaded_files_upload_file_id";
DROP INDEX IF EXISTS "idx_user_model_provider_group_models_provider";
DROP INDEX IF EXISTS "idx_user_model_provider_groups_parent";
DROP INDEX IF EXISTS "idx_user_schedules_group_id";
DROP INDEX IF EXISTS "idx_word_column";
DROP INDEX IF EXISTS "idx_word_create_user_group_id";
DROP INDEX IF EXISTS "idx_word_group_conflict_user_updated";
DROP INDEX IF EXISTS "uk_agent_thread_records_record_key";
DROP INDEX IF EXISTS "uk_conversation_idle_events_event_id";
DROP INDEX IF EXISTS "uk_default_model_providers_name";
DROP INDEX IF EXISTS "uk_default_models_provider_name";
DROP INDEX IF EXISTS "uk_local_fs_chat_settings_user";
DROP INDEX IF EXISTS "uk_personal_resource_revisions_no";
DROP INDEX IF EXISTS "uk_personal_resources_user_type";
DROP INDEX IF EXISTS "uk_plugin_revisions_resource_no";
DROP INDEX IF EXISTS "uk_plugin_step_intent";
DROP INDEX IF EXISTS "uk_prompt_user_states_user_prompt";
DROP INDEX IF EXISTS "uk_resource_session_snapshots";
DROP INDEX IF EXISTS "uk_skill_draft_review_batch_sequence";
DROP INDEX IF EXISTS "uk_skill_revisions_skill_no";
DROP INDEX IF EXISTS "uk_skills_owner_identity";
DROP INDEX IF EXISTS "uk_skills_owner_relative_root";
DROP INDEX IF EXISTS "uk_task_run_input_snapshot";
DROP INDEX IF EXISTS "uk_user_disabled_tools_user_tool";
DROP INDEX IF EXISTS "uk_user_model_provider_group_models_group_name";
DROP INDEX IF EXISTS "uk_user_personalization_settings_user_id";
DROP INDEX IF EXISTS "uk_user_selected_models_user_type";
DROP INDEX IF EXISTS "uk_user_selected_providers_user_category";
DROP INDEX IF EXISTS "ukx_create_user_id_dataset_id";
DROP INDEX IF EXISTS "uniq_resource_update_active_auto_apply_result";
DROP INDEX IF EXISTS "uniq_resource_update_task_trigger";

CREATE TABLE IF NOT EXISTS `acl_groups` (`id` varchar(255),`name` varchar(255) NOT NULL DEFAULT "",PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `acl_kbs` (`id` varchar(64),`name` varchar(255),`owner_id` varchar(255),`visibility` varchar(32),PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `acl_rows` (`id` integer PRIMARY KEY AUTOINCREMENT,`resource_type` varchar(32),`resource_id` varchar(255),`grantee_type` varchar(32),`target_id` varchar(255),`permission` varchar(32),`created_by` varchar(255),`created_at` datetime,`expires_at` datetime);

CREATE TABLE IF NOT EXISTS `acl_user_groups` (`user_id` varchar(255),`group_id` varchar(255),PRIMARY KEY (`user_id`,`group_id`));

CREATE TABLE IF NOT EXISTS `acl_visibility` (`id` integer PRIMARY KEY AUTOINCREMENT,`resource_id` varchar(255),`level` varchar(32));

CREATE TABLE IF NOT EXISTS `agent_thread_records` (`id` varchar(32),`thread_id` varchar(128) NOT NULL,`round_id` varchar(32) NOT NULL DEFAULT "",`task_id` varchar(128) NOT NULL DEFAULT "",`stream_kind` varchar(32) NOT NULL,`record_key` varchar(64) NOT NULL,`event_name` varchar(128) NOT NULL DEFAULT "",`payload_text` text NOT NULL DEFAULT "",`raw_frame` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `agent_thread_rounds` (`round_id` varchar(32),`thread_id` varchar(128) NOT NULL,`request_hash` varchar(64) NOT NULL DEFAULT "",`task_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "created",`user_message` text NOT NULL DEFAULT "",`assistant_message` text NOT NULL DEFAULT "",`request_payload` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`round_id`));

CREATE TABLE IF NOT EXISTS `agent_threads` (`thread_id` varchar(128),`current_task_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "created",`thread_payload` text NOT NULL DEFAULT "",`last_message_request_hash` varchar(64) NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL DEFAULT "",`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`thread_id`));

CREATE TABLE IF NOT EXISTS `agent_user_active_threads` (`user_id` varchar(255),`thread_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "creating",`create_token` varchar(64) NOT NULL DEFAULT "",`lease_until` datetime NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`user_id`));

CREATE TABLE IF NOT EXISTS `chat_histories` (`id` varchar(36),`seq` integer NOT NULL,`conversation_id` varchar(36) NOT NULL,`raw_content` text,`retrieval_result` json,`content` text,`result` text,`feed_back` integer DEFAULT 0,`reason` varchar(255),`expected_answer` text,`ext` json,`version` varchar(128) DEFAULT "2.3",`create_time` datetime NOT NULL,`update_time` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `conversations` (`id` varchar(36),`display_name` varchar(255),`channel_id` varchar(36) NOT NULL DEFAULT "default",`search_config` json,`application_id` varchar(64) DEFAULT "",`ext` json,`model` varchar(64) DEFAULT "",`models` json,`chat_times` integer NOT NULL DEFAULT 0,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `datasets` (`id` varchar(255),`kb_id` varchar(255) NOT NULL,`display_name` varchar(255) NOT NULL,`desc` longtext NOT NULL,`cover_image` varchar(255) NOT NULL,`resource_uid` varchar(36) NOT NULL,`bucket_name` varchar(255) NOT NULL,`oss_path` varchar(255) NOT NULL,`dataset_info` json,`dataset_state` integer NOT NULL,`embedding_model` varchar(255) NOT NULL,`embedding_model_provider` varchar(255) NOT NULL,`share_type` integer NOT NULL,`shared_at` datetime,`tenant_id` varchar(36) NOT NULL,`is_demonstrate` numeric NOT NULL DEFAULT false,`type` integer NOT NULL DEFAULT 1,`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_datasets` (`id` integer PRIMARY KEY AUTOINCREMENT,`dataset_id` varchar(64) NOT NULL,`dataset_name` varchar(255) NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `default_model_providers` (`id` varchar(64),`name` varchar(255) NOT NULL,`description` text NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_models` (`id` varchar(64),`default_model_provider_id` varchar(64) NOT NULL,`provider_name` varchar(255) NOT NULL DEFAULT "",`name` varchar(512) NOT NULL,`model_type` varchar(64) NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_prompts` (`id` integer PRIMARY KEY AUTOINCREMENT,`prompt_id` varchar(64) NOT NULL,`prompt_name` varchar(255) NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `documents` (`id` varchar(128),`lazyllm_doc_id` varchar(128) NOT NULL DEFAULT "",`dataset_id` varchar(255) NOT NULL,`display_name` varchar(512) NOT NULL DEFAULT "",`p_id` varchar(255) NOT NULL DEFAULT "",`tags` json,`file_id` varchar(128) NOT NULL DEFAULT "",`pdf_convert_result` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `multi_answers_chat_histories` (`id` varchar(36),`seq` integer NOT NULL,`conversation_id` varchar(36) NOT NULL,`raw_content` text,`retrieval_result` json,`content` text,`result` text,`feed_back` integer DEFAULT 0,`reason` varchar(255),`ext` json,`endpoint` varchar(512),`create_time` datetime NOT NULL,`update_time` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `multi_answers_switches` (`id` integer PRIMARY KEY AUTOINCREMENT,`status` integer NOT NULL DEFAULT 0,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `prompts` (`id` varchar(64),`name` varchar(255) NOT NULL,`content` text NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `resource_session_snapshots` (`id` varchar(36),`session_id` varchar(128) NOT NULL,`user_id` varchar(255) NOT NULL DEFAULT "",`resource_type` varchar(32) NOT NULL,`resource_key` varchar(1024) NOT NULL,`category` varchar(128) NOT NULL DEFAULT "",`parent_skill_name` varchar(255) NOT NULL DEFAULT "",`skill_name` varchar(255) NOT NULL DEFAULT "",`file_ext` varchar(32) NOT NULL DEFAULT "",`relative_path` varchar(1024) NOT NULL DEFAULT "",`snapshot_hash` varchar(64) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `resource_suggestions` (`id` varchar(36),`user_id` varchar(255) NOT NULL DEFAULT "",`resource_type` varchar(32) NOT NULL,`resource_key` varchar(1024) NOT NULL DEFAULT "",`category` varchar(128) NOT NULL DEFAULT "",`parent_skill_name` varchar(255) NOT NULL DEFAULT "",`skill_name` varchar(255) NOT NULL DEFAULT "",`file_ext` varchar(32) NOT NULL DEFAULT "",`relative_path` varchar(1024) NOT NULL DEFAULT "",`action` varchar(32) NOT NULL,`session_id` varchar(128) NOT NULL,`snapshot_hash` varchar(64) NOT NULL DEFAULT "",`title` varchar(255) NOT NULL DEFAULT "",`content` text,`reason` text,`full_content` text,`status` varchar(32) NOT NULL,`invalid_reason` text,`reviewer_id` varchar(255) NOT NULL DEFAULT "",`reviewer_name` varchar(255) NOT NULL DEFAULT "",`reviewed_at` datetime,`ext` json,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_resources` (`id` varchar(36),`owner_user_id` varchar(255) NOT NULL,`owner_user_name` varchar(255) NOT NULL DEFAULT "",`category` varchar(128) NOT NULL,`parent_skill_name` varchar(255) NOT NULL DEFAULT "",`skill_name` varchar(255) NOT NULL DEFAULT "",`node_type` varchar(32) NOT NULL,`description` text,`tags` json,`file_ext` varchar(32) NOT NULL DEFAULT "md",`relative_path` varchar(1024) NOT NULL,`content` text NOT NULL DEFAULT "",`content_size` integer NOT NULL DEFAULT 0,`mime_type` varchar(128) NOT NULL DEFAULT "text/plain",`content_hash` varchar(64) NOT NULL DEFAULT "",`version` integer NOT NULL DEFAULT 1,`draft_content` text NOT NULL DEFAULT "",`draft_source_version` integer NOT NULL DEFAULT 0,`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`auto_evo` numeric NOT NULL DEFAULT false,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`is_enabled` numeric NOT NULL DEFAULT true,`update_status` varchar(32) NOT NULL DEFAULT "up_to_date",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_share_items` (`id` varchar(36),`share_task_id` varchar(36) NOT NULL,`target_user_id` varchar(255) NOT NULL,`target_user_name` varchar(255) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL,`target_relative_root` varchar(1024) NOT NULL DEFAULT "",`accepted_at` datetime,`rejected_at` datetime,`target_root_skill_id` varchar(36) NOT NULL DEFAULT "",`error_message` text,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_share_tasks` (`id` varchar(36),`source_user_id` varchar(255) NOT NULL,`source_user_name` varchar(255) NOT NULL DEFAULT "",`source_skill_id` varchar(36) NOT NULL,`source_category` varchar(128) NOT NULL DEFAULT "",`source_parent_skill_name` varchar(255) NOT NULL DEFAULT "",`source_relative_root` varchar(1024) NOT NULL DEFAULT "",`message` text,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `system_memories` (`id` varchar(36),`user_id` varchar(255) NOT NULL DEFAULT "",`content` text NOT NULL DEFAULT "",`content_hash` varchar(64) NOT NULL DEFAULT "",`version` integer NOT NULL DEFAULT 1,`draft_content` text,`draft_source_version` integer NOT NULL DEFAULT 0,`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`auto_evo` numeric NOT NULL DEFAULT true,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`ext` json,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `system_user_preferences` (`id` varchar(36),`user_id` varchar(255) NOT NULL DEFAULT "",`content` text NOT NULL DEFAULT "",`content_hash` varchar(64) NOT NULL DEFAULT "",`version` integer NOT NULL DEFAULT 1,`draft_content` text,`draft_source_version` integer NOT NULL DEFAULT 0,`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`auto_evo` numeric NOT NULL DEFAULT true,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`ext` json,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `tasks` (`id` varchar(128),`lazyllm_task_id` varchar(128) NOT NULL DEFAULT "",`doc_id` varchar(128),`kb_id` varchar(255),`algo_id` varchar(255),`dataset_id` varchar(255) NOT NULL,`task_type` varchar(128) NOT NULL DEFAULT "",`document_pid` varchar(255) NOT NULL DEFAULT "",`target_pid` varchar(255) NOT NULL DEFAULT "",`target_dataset_id` varchar(255) NOT NULL DEFAULT "",`display_name` varchar(512) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `upload_sessions` (`id` integer PRIMARY KEY AUTOINCREMENT,`upload_id` varchar(128) NOT NULL,`task_id` varchar(128) NOT NULL,`dataset_id` varchar(255) NOT NULL,`tenant_id` varchar(36) NOT NULL,`document_id` varchar(128) NOT NULL,`upload_state` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `uploaded_files` (`id` integer PRIMARY KEY AUTOINCREMENT,`upload_file_id` varchar(128) NOT NULL,`dataset_id` varchar(255) NOT NULL,`tenant_id` varchar(36) NOT NULL,`task_id` varchar(128) NOT NULL DEFAULT "",`document_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `user_model_provider_group_models` (`id` varchar(64),`user_model_provider_id` varchar(64) NOT NULL,`user_model_provider_group_id` varchar(64) NOT NULL,`provider_name` varchar(255) NOT NULL DEFAULT "",`name` varchar(512) NOT NULL,`model_type` varchar(64) NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`is_default` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_model_provider_groups` (`id` varchar(64),`user_model_provider_id` varchar(64) NOT NULL,`name` varchar(255) NOT NULL,`base_url` varchar(1024) NOT NULL,`api_key` text NOT NULL,`is_verified` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_model_providers` (`id` varchar(64),`default_model_provider_id` varchar(64) NOT NULL,`name` varchar(255) NOT NULL,`description` text NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_personalization_settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` varchar(255) NOT NULL,`enabled` numeric NOT NULL DEFAULT true,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `user_selected_models` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` varchar(255) NOT NULL,`user_name` varchar(255) NOT NULL DEFAULT "",`model_type` varchar(64) NOT NULL,`user_model_provider_group_model_id` varchar(64) NOT NULL,`share` boolean NOT NULL DEFAULT false,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `word_group_conflicts` (`id` varchar(64),`reason` text NOT NULL DEFAULT "",`word` text NOT NULL DEFAULT "",`description` text NOT NULL DEFAULT "",`group_ids` text NOT NULL DEFAULT "[]",`create_user_id` varchar(255) NOT NULL,`message_ids` text NOT NULL DEFAULT "[]",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `words` (`id` varchar(64),`word` varchar(512) NOT NULL,`word_kind` varchar(32) NOT NULL DEFAULT "term",`group_id` varchar(64) NOT NULL,`description` varchar(512) NOT NULL DEFAULT "",`source` varchar(32) NOT NULL DEFAULT "user",`reference_info` text NOT NULL DEFAULT "",`locked` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE INDEX IF NOT EXISTS `idx_acl_resource` ON `acl_rows`(`resource_type`,`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_acl_visibility_resource_id` ON `acl_visibility`(`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_round_stream_id` ON `agent_thread_records`(`round_id`,`stream_kind`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_task_id` ON `agent_thread_records`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_thread_round_id` ON `agent_thread_records`(`thread_id`,`round_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_thread_stream_id` ON `agent_thread_records`(`thread_id`,`stream_kind`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_task_id` ON `agent_thread_rounds`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_thread_id` ON `agent_thread_rounds`(`thread_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_thread_request_hash` ON `agent_thread_rounds`(`thread_id`,`request_hash`);

CREATE INDEX IF NOT EXISTS `idx_agent_threads_current_task_id` ON `agent_threads`(`current_task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_user_active_threads_status_lease` ON `agent_user_active_threads`(`status`,`lease_until`);

CREATE INDEX IF NOT EXISTS `idx_agent_user_active_threads_thread_id` ON `agent_user_active_threads`(`thread_id`);

CREATE INDEX IF NOT EXISTS `idx_chat_histories_conversation_id` ON `chat_histories`(`conversation_id`);

CREATE INDEX IF NOT EXISTS `idx_datasets_kb_id` ON `datasets`(`kb_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_dataset_id` ON `documents`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_lazyllm_doc_id` ON `documents`(`lazyllm_doc_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_p_id` ON `documents`(`p_id`);

CREATE INDEX IF NOT EXISTS `idx_multi_answers_chat_histories_conversation_id` ON `multi_answers_chat_histories`(`conversation_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_session_snapshots_session_id` ON `resource_session_snapshots`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_suggestions_list` ON `resource_suggestions`(`user_id`,`resource_type`,`status`);

CREATE INDEX IF NOT EXISTS `idx_resource_suggestions_session_id` ON `resource_suggestions`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_uid` ON `datasets`(`resource_uid`);

CREATE INDEX IF NOT EXISTS `idx_skill_resources_owner_node_enabled` ON `skill_resources`(`owner_user_id`,`node_type`,`is_enabled`,`category`);

CREATE INDEX IF NOT EXISTS `idx_skill_share_items_target_user` ON `skill_share_items`(`share_task_id`,`target_user_id`,`status`);

CREATE INDEX IF NOT EXISTS `idx_skill_share_tasks_source_user` ON `skill_share_tasks`(`source_user_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_algo_id` ON `tasks`(`algo_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_dataset_id` ON `tasks`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_doc_id` ON `tasks`(`doc_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_document_p_id` ON `tasks`(`document_pid`);

CREATE INDEX IF NOT EXISTS `idx_tasks_kb_id` ON `tasks`(`kb_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_lazyllm_task_id` ON `tasks`(`lazyllm_task_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_target_dataset_id` ON `tasks`(`target_dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_task_type` ON `tasks`(`task_type`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_dataset_id` ON `upload_sessions`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_document_id` ON `upload_sessions`(`document_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_task_id` ON `upload_sessions`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_tenant_id` ON `upload_sessions`(`tenant_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_upload_sessions_upload_id` ON `upload_sessions`(`upload_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_upload_state` ON `upload_sessions`(`upload_state`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_dataset_id` ON `uploaded_files`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_document_id` ON `uploaded_files`(`document_id`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_status` ON `uploaded_files`(`status`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_task_id` ON `uploaded_files`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_tenant_id` ON `uploaded_files`(`tenant_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_uploaded_files_upload_file_id` ON `uploaded_files`(`upload_file_id`);

CREATE INDEX IF NOT EXISTS `idx_user_model_provider_group_models_provider` ON `user_model_provider_group_models`(`user_model_provider_id`);

CREATE INDEX IF NOT EXISTS `idx_user_model_provider_groups_parent` ON `user_model_provider_groups`(`user_model_provider_id`);

CREATE INDEX IF NOT EXISTS `idx_word_column` ON `words`(`create_user_id`,`word`);

CREATE INDEX IF NOT EXISTS `idx_word_create_user_group_id` ON `words`(`create_user_id`,`group_id`);

CREATE INDEX IF NOT EXISTS `idx_word_group_conflict_user_updated` ON `word_group_conflicts`(`create_user_id`,`updated_at` desc);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_agent_thread_records_record_key` ON `agent_thread_records`(`thread_id`,`round_id`,`stream_kind`,`record_key`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_default_model_providers_name` ON `default_model_providers`(`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_default_models_provider_name` ON `default_models`(`default_model_provider_id`,`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_resource_session_snapshots` ON `resource_session_snapshots`(`session_id`,`resource_type`,`resource_key`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_skill_resources_owner_relative_path` ON `skill_resources`(`owner_user_id`,`relative_path`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_system_memories_user_id` ON `system_memories`(`user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_system_user_preferences_user_id` ON `system_user_preferences`(`user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_model_provider_group_models_group_name` ON `user_model_provider_group_models`(`user_model_provider_group_id`,`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_personalization_settings_user_id` ON `user_personalization_settings`(`user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_selected_models_user_type` ON `user_selected_models`(`user_id`,`model_type`);

CREATE UNIQUE INDEX IF NOT EXISTS `ukx_create_user_id_dataset_id` ON `default_datasets`(`dataset_id`);

INSERT INTO "acl_groups" (id,name) SELECT id,name FROM "__v02_acl_groups";
INSERT INTO "acl_kbs" (id,name,owner_id,visibility) SELECT id,name,owner_id,visibility FROM "__v02_acl_kbs";
INSERT INTO "acl_rows" (id,resource_type,resource_id,grantee_type,target_id,permission,created_by,created_at,expires_at) SELECT id,resource_type,resource_id,grantee_type,target_id,permission,created_by,created_at,expires_at FROM "__v02_acl_rows";
INSERT INTO "acl_user_groups" (user_id,group_id) SELECT user_id,group_id FROM "__v02_acl_user_groups";
INSERT INTO "acl_visibility" (id,resource_id,level) SELECT id,resource_id,level FROM "__v02_acl_visibility";
INSERT INTO "agent_thread_records" (id,thread_id,round_id,task_id,stream_kind,record_key,event_name,payload_text,raw_frame,created_at,updated_at) SELECT id,thread_id,round_id,task_id,stream_kind,record_key,event_name,payload_text,raw_frame,created_at,updated_at FROM "__v02_agent_thread_records";
INSERT INTO "agent_thread_rounds" (round_id,thread_id,request_hash,task_id,status,user_message,assistant_message,request_payload,created_at,updated_at) SELECT round_id,thread_id,request_hash,task_id,status,user_message,assistant_message,request_payload,created_at,updated_at FROM "__v02_agent_thread_rounds";
INSERT INTO "agent_threads" (thread_id,current_task_id,status,thread_payload,last_message_request_hash,create_user_id,create_user_name,created_at,updated_at) SELECT thread_id,current_task_id,status,thread_payload,last_message_request_hash,create_user_id,create_user_name,created_at,updated_at FROM "__v02_agent_threads";
INSERT INTO "agent_user_active_threads" (user_id,thread_id,status,create_token,lease_until,created_at,updated_at) SELECT user_id,thread_id,status,create_token,lease_until,created_at,updated_at FROM "__v02_agent_user_active_threads";
INSERT INTO "chat_histories" (id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,expected_answer,ext,version,create_time,update_time) SELECT id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,expected_answer,ext,version,create_time,update_time FROM "__v02_chat_histories";
INSERT INTO "conversations" (id,display_name,channel_id,search_config,application_id,ext,model,models,chat_times,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,display_name,channel_id,search_config,application_id,ext,model,models,chat_times,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_conversations";
INSERT INTO "datasets" (id,kb_id,display_name,desc,cover_image,resource_uid,bucket_name,oss_path,dataset_info,dataset_state,embedding_model,embedding_model_provider,share_type,shared_at,tenant_id,is_demonstrate,type,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,kb_id,display_name,desc,cover_image,resource_uid,bucket_name,oss_path,dataset_info,dataset_state,embedding_model,embedding_model_provider,share_type,shared_at,tenant_id,is_demonstrate,type,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_datasets";
INSERT INTO "default_datasets" (id,dataset_id,dataset_name,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,dataset_id,dataset_name,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_default_datasets";
INSERT INTO "default_model_providers" (id,name,description,base_url,created_at,updated_at,deleted_at) SELECT id,name,description,base_url,created_at,updated_at,deleted_at FROM "__v02_default_model_providers";
INSERT INTO "default_models" (id,default_model_provider_id,provider_name,name,model_type,created_at,updated_at,deleted_at) SELECT id,default_model_provider_id,provider_name,name,model_type,created_at,updated_at,deleted_at FROM "__v02_default_models";
INSERT INTO "documents" (id,lazyllm_doc_id,dataset_id,display_name,p_id,tags,file_id,pdf_convert_result,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,lazyllm_doc_id,dataset_id,display_name,p_id,tags,file_id,pdf_convert_result,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_documents";
INSERT INTO "multi_answers_chat_histories" (id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,ext,endpoint,create_time,update_time) SELECT id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,ext,endpoint,create_time,update_time FROM "__v02_multi_answers_chat_histories";
INSERT INTO "multi_answers_switches" (id,status,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,status,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_multi_answers_switches";
INSERT INTO "prompts" (id,name,content,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,name,content,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_prompts";
INSERT INTO "resource_session_snapshots" (id,session_id,user_id,resource_type,resource_key,category,parent_skill_name,skill_name,file_ext,relative_path,snapshot_hash,created_at) SELECT id,session_id,user_id,resource_type,resource_key,category,parent_skill_name,skill_name,file_ext,relative_path,snapshot_hash,created_at FROM "__v02_resource_session_snapshots";
INSERT INTO "skill_share_items" (id,share_task_id,target_user_id,target_user_name,status,target_relative_root,accepted_at,rejected_at,target_root_skill_id,error_message,created_at,updated_at) SELECT id,share_task_id,target_user_id,target_user_name,status,target_relative_root,accepted_at,rejected_at,target_root_skill_id,error_message,created_at,updated_at FROM "__v02_skill_share_items";
INSERT INTO "skill_share_tasks" (id,source_user_id,source_user_name,source_skill_id,source_category,source_parent_skill_name,source_relative_root,message,created_at,updated_at) SELECT id,source_user_id,source_user_name,source_skill_id,source_category,source_parent_skill_name,source_relative_root,message,created_at,updated_at FROM "__v02_skill_share_tasks";
INSERT INTO "tasks" (id,lazyllm_task_id,doc_id,kb_id,algo_id,dataset_id,task_type,document_pid,target_pid,target_dataset_id,display_name,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,lazyllm_task_id,doc_id,kb_id,algo_id,dataset_id,task_type,document_pid,target_pid,target_dataset_id,display_name,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_tasks";
INSERT INTO "upload_sessions" (id,upload_id,task_id,dataset_id,tenant_id,document_id,upload_state,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,upload_id,task_id,dataset_id,tenant_id,document_id,upload_state,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_upload_sessions";
INSERT INTO "uploaded_files" (id,upload_file_id,dataset_id,tenant_id,task_id,document_id,status,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,upload_file_id,dataset_id,tenant_id,task_id,document_id,status,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_uploaded_files";
INSERT INTO "user_model_provider_group_models" (id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_user_model_provider_group_models";
INSERT INTO "user_model_provider_groups" (id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_user_model_provider_groups";
INSERT INTO "user_model_providers" (id,default_model_provider_id,name,description,base_url,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,default_model_provider_id,name,description,base_url,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_user_model_providers";
INSERT INTO "user_personalization_settings" (id,user_id,enabled,updated_by,updated_by_name,created_at,updated_at) SELECT id,user_id,enabled,updated_by,updated_by_name,created_at,updated_at FROM "__v02_user_personalization_settings";
INSERT INTO "user_selected_models" (id,user_id,user_name,model_type,user_model_provider_group_model_id,share,created_at,updated_at) SELECT id,user_id,user_name,model_type,user_model_provider_group_model_id,share,created_at,updated_at FROM "__v02_user_selected_models";
INSERT INTO "word_group_conflicts" (id,reason,word,description,group_ids,create_user_id,message_ids,created_at,updated_at,deleted_at) SELECT id,reason,word,description,group_ids,create_user_id,message_ids,created_at,updated_at,deleted_at FROM "__v02_word_group_conflicts";
INSERT INTO "words" (id,word,word_kind,group_id,description,source,reference_info,locked,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,word,word_kind,group_id,description,source,reference_info,locked,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_words";
DROP TABLE "__v02_acl_groups";
DROP TABLE "__v02_acl_kbs";
DROP TABLE "__v02_acl_rows";
DROP TABLE "__v02_acl_user_groups";
DROP TABLE "__v02_acl_visibility";
DROP TABLE "__v02_agent_thread_records";
DROP TABLE "__v02_agent_thread_rounds";
DROP TABLE "__v02_agent_thread_steps";
DROP TABLE "__v02_agent_threads";
DROP TABLE "__v02_agent_user_active_threads";
DROP TABLE "__v02_async_jobs";
DROP TABLE "__v02_automation_groups";
DROP TABLE "__v02_chat_histories";
DROP TABLE "__v02_conversation_artifacts";
DROP TABLE "__v02_conversation_idle_events";
DROP TABLE "__v02_conversations";
DROP TABLE "__v02_datasets";
DROP TABLE "__v02_default_datasets";
DROP TABLE "__v02_default_model_providers";
DROP TABLE "__v02_default_models";
DROP TABLE "__v02_documents";
DROP TABLE "__v02_eval_set_import_previews";
DROP TABLE "__v02_eval_set_items";
DROP TABLE "__v02_eval_set_shards";
DROP TABLE "__v02_eval_sets";
DROP TABLE "__v02_external_database_connections";
DROP TABLE "__v02_local_fs_chat_settings";
DROP TABLE "__v02_mcp_server_tools";
DROP TABLE "__v02_mcp_servers";
DROP TABLE "__v02_memory_review";
DROP TABLE "__v02_multi_answers_chat_histories";
DROP TABLE "__v02_multi_answers_switches";
DROP TABLE "__v02_personal_resource_blobs";
DROP TABLE "__v02_personal_resource_drafts";
DROP TABLE "__v02_personal_resource_review_action_batches";
DROP TABLE "__v02_personal_resource_review_action_items";
DROP TABLE "__v02_personal_resource_review_sessions";
DROP TABLE "__v02_personal_resource_revisions";
DROP TABLE "__v02_personal_resources";
DROP TABLE "__v02_plugin_attempt_input_bindings";
DROP TABLE "__v02_plugin_blobs";
DROP TABLE "__v02_plugin_drafts";
DROP TABLE "__v02_plugin_human_artifacts";
DROP TABLE "__v02_plugin_revision_entries";
DROP TABLE "__v02_plugin_revisions";
DROP TABLE "__v02_plugin_route_decisions";
DROP TABLE "__v02_plugin_run_outbox";
DROP TABLE "__v02_plugin_session_steps";
DROP TABLE "__v02_plugin_sessions";
DROP TABLE "__v02_plugin_slot_order";
DROP TABLE "__v02_plugin_slot_revisions";
DROP TABLE "__v02_plugin_step_intents";
DROP TABLE "__v02_plugin_transition_commands";
DROP TABLE "__v02_plugins";
DROP TABLE "__v02_prompt_categories";
DROP TABLE "__v02_prompt_user_states";
DROP TABLE "__v02_prompts";
DROP TABLE "__v02_resource_session_snapshots";
DROP TABLE "__v02_resource_update_tasks";
DROP TABLE "__v02_schedule_dependencies";
DROP TABLE "__v02_skill_blobs";
DROP TABLE "__v02_skill_draft_entries";
DROP TABLE "__v02_skill_draft_review_action_batches";
DROP TABLE "__v02_skill_draft_review_action_items";
DROP TABLE "__v02_skill_draft_review_sessions";
DROP TABLE "__v02_skill_drafts";
DROP TABLE "__v02_skill_market_items";
DROP TABLE "__v02_skill_review_results";
DROP TABLE "__v02_skill_review_scheduler_state";
DROP TABLE "__v02_skill_review_stats";
DROP TABLE "__v02_skill_revision_entries";
DROP TABLE "__v02_skill_revisions";
DROP TABLE "__v02_skill_search_indexes";
DROP TABLE "__v02_skill_share_items";
DROP TABLE "__v02_skill_share_tasks";
DROP TABLE "__v02_skills";
DROP TABLE "__v02_sub_agent_artifacts";
DROP TABLE "__v02_sub_agent_steps";
DROP TABLE "__v02_sub_agent_tasks";
DROP TABLE "__v02_task_center_tasks";
DROP TABLE "__v02_task_run_inputs";
DROP TABLE "__v02_task_run_outputs";
DROP TABLE "__v02_tasks";
DROP TABLE "__v02_upload_sessions";
DROP TABLE "__v02_uploaded_files";
DROP TABLE "__v02_user_chat_settings";
DROP TABLE "__v02_user_disabled_tools";
DROP TABLE "__v02_user_model_provider_group_models";
DROP TABLE "__v02_user_model_provider_groups";
DROP TABLE "__v02_user_model_providers";
DROP TABLE "__v02_user_personalization_settings";
DROP TABLE "__v02_user_plugin_settings";
DROP TABLE "__v02_user_schedules";
DROP TABLE "__v02_user_selected_models";
DROP TABLE "__v02_user_selected_providers";
DROP TABLE "__v02_user_ui_preferences";
DROP TABLE "__v02_word_group_conflicts";
DROP TABLE "__v02_words";

UPDATE default_models SET model_type = CASE model_type
  WHEN 'vlm' THEN 'VLM' WHEN 'embed' THEN 'embedding'
  WHEN 'cross_modal_embed' THEN 'multimodal_embedding' WHEN 'reranker' THEN 'rerank'
  ELSE model_type END
WHERE model_type IN ('vlm','embed','cross_modal_embed','reranker');
UPDATE user_model_provider_group_models SET model_type = CASE model_type
  WHEN 'vlm' THEN 'VLM' WHEN 'embed' THEN 'embedding'
  WHEN 'cross_modal_embed' THEN 'multimodal_embedding' WHEN 'reranker' THEN 'rerank'
  ELSE model_type END
WHERE model_type IN ('vlm','embed','cross_modal_embed','reranker');
UPDATE user_selected_models SET model_type = CASE model_type
  WHEN 'llm' THEN 'llm-chat' WHEN 'evo_llm' THEN 'llm-evo' WHEN 'vlm' THEN 'VLM'
  WHEN 'embed_main' THEN 'embedding' WHEN 'embed_image' THEN 'multimodal_embedding'
  WHEN 'reranker' THEN 'rerank' ELSE model_type END
WHERE model_type IN ('llm','evo_llm','vlm','embed_main','embed_image','reranker');
