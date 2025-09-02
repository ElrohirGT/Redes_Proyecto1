{
  lib,
  buildGoModule,
}:
buildGoModule {
  name = "CLIude";
  src = ./.;
  vendorHash = "sha256-psz4szKoaxz14QV+RJRDWjKxR3Z3oQSGTzAO6oQ0L5M=";
  doCheck = true;
  meta = {
    description = "Unnoficial TUI for Claude";
    homepage = "https://github.com/ElrohirGT/Redes_Proyecto1";
    license = lib.licenses.mit;
    maintainers = with lib.maintainers; [elrohirgt];
  };
}
