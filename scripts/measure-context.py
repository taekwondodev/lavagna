# /// script
# dependencies = ["tiktoken==0.14.0"]
# ///
"""Run with uv run scripts/measure-context.py. No product dependency is added.

Replays one grilling phase, the five-call walk of the call model (#15), against
the pinned baseline and the current checkout, and fails when the savings settled
in #21 are not met. Counts the agent's command line and stdin plus stdout AND
stderr of every call, feedback reads and `round --help` once per phase, with
o200k_base. HTTP traffic and browser rendering are not agent context.
"""

import io
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

import tiktoken

ROOT = Path(__file__).resolve().parents[1]
# main before per-question rounds: one complete round per turn, --reuse when
# the content is unchanged.
BASE = "5f7f83943a537cb31e6e0b9705cdbcc00bd8f421"
ENC = tiktoken.get_encoding("o200k_base")

# Thresholds settled in #21.
PHASE_SHARE = 0.50
REPLY_SHARE = 0.10
FIRST_SHARE = 1.10


def question(qid, title, capire, options, reason):
    return dict(id=qid, title=title, capire=capire.strip(), options=options, reason=reason)


def option(oid, label, detail, effect, recommended=False):
    return dict(id=oid, label=label, detail=detail, effect=effect, recommended=recommended)


STORAGE = question("storage", "Dove salviamo lo stato delle sessioni?", """
Oggi tutte le sessioni scrivono lo stesso `state.json` nella cartella di cache. Ogni scrittura riscrive il file intero, quindi due sessioni aperte insieme si sovrascrivono a vicenda: l'ultima che salva vince e l'altra perde le proprie modifiche senza alcun avviso.

Il problema emerge solo con più sessioni contemporanee, ma è proprio il caso che vogliamo supportare: un utente apre due conversazioni sullo stesso progetto e le porta avanti in parallelo per tutta la giornata.

::: evidence
- [observed] Con due sessioni aperte, la seconda scrittura cancella la prima: lo stato della sessione A sparisce dopo un salvataggio della sessione B.
- [observed] Il file pesa tra 2 e 40 KB nelle sessioni reali della settimana scorsa.
- [unverified] Le scritture concorrenti sono rare fuori da questo scenario.
:::

| Criterio | File per sessione | Database locale |
| --- | --- | --- |
| Contesa | nessuna, ogni sessione ha il suo file | gestita dal database con transazioni |
| Dipendenze | nessuna nuova | un driver e una migrazione dello schema |
| Pulizia | un file da eliminare per sessione | righe da eliminare con una query |
| Ripristino | si cancella il file rotto | serve uno strumento di riparazione |

### Come funziona oggi
Il comando apre `state.json`, lo decodifica per intero, applica la modifica e lo riscrive da capo. Non esiste alcun lock tra processi: il sistema operativo serializza le singole scritture, ma non la sequenza leggi, modifica, scrivi. Due sessioni che leggono la stessa versione e poi salvano producono una perdita silenziosa, che nessun log registra.

- La sessione A legge lo stato con tre conversazioni aperte.
- La sessione B legge lo stesso stato e aggiunge una quarta conversazione.
- La sessione A salva la propria modifica e cancella la quarta conversazione di B.
- B non vede alcun errore: scopre la perdita solo alla riapertura.

Il formato del contenuto non cambia: lo stesso JSON di oggi, una copia per sessione invece di un documento unico. Gli strumenti che leggono `state.json` per diagnosi dovranno cercare nella cartella delle sessioni, ed è l'unico consumatore esterno che conosciamo.

Con un file per sessione la sequenza non condivide più alcuna risorsa, quindi il problema scompare senza introdurre lock.

Un file per sessione elimina la contesa alla radice e non aggiunge dipendenze. Il database coordina scritture condivise che oggi non abbiamo, al prezzo di un formato da migrare e di un processo in più da gestire.
""", [
    option("file", "Un file per sessione", "Nessuna contesa e nessuna dipendenza; più file da pulire.", "Ogni sessione scrive solo il proprio file.", True),
    option("db", "Database locale", "Scritture coordinate; una dipendenza e una migrazione.", "Le sessioni condividono una tabella con transazioni."),
], "Nessuna dipendenza nuova e nessuna attesa tra sessioni.")

RETENTION_CAPIRE = """
Le sessioni chiuse restano su disco finché qualcuno non le elimina. In un mese di uso normale la cartella supera i 300 file, e nessun comando oggi li rimuove: l'utente li scopre solo quando lo spazio di cache diventa un problema.

Una sessione si considera inattiva quando nessun comando la usa. Il conteggio riparte a ogni uso, quindi una sessione ripresa resta viva finché qualcuno continua a lavorarci, indipendentemente da quando è stata creata.

::: evidence
- [observed] Il 70 % delle sessioni non viene più aperto dopo il primo giorno.
- [observed] Una sessione su dieci viene ripresa dopo un fine settimana.
- [unverified] Le sessioni riprese dopo più di una settimana sono quasi assenti.
:::

| Criterio | 1 giorno | 7 giorni |
| --- | --- | --- |
| Spazio occupato | minimo | circa sette volte tanto |
| Sessioni riprese dopo il weekend | perse | conservate |
| Prevedibilità | alta | alta |

### Cosa conta come uso
Ogni comando che apre la sessione aggiorna la data di ultimo uso, anche una sola lettura. Non aggiorniamo la data quando lo sweep elenca le sessioni, altrimenti nessuna scadrebbe mai. La data vive nei metadati del file, così non serve riscrivere il contenuto per rinnovarla.

- Aprire la pagina di una sessione rinnova la data.
- Leggere il feedback trattenuto rinnova la data.
- Elencare le sessioni per la pulizia non la rinnova.
- Copiare la cartella con strumenti esterni può alterarla, ed è un caso che accettiamo.

Una sessione eliminata non si recupera: i file sono privati e non esiste un cestino. Per questo la finestra va scelta sul caso peggiore realistico, non sulla media, e il caso peggiore che osserviamo è il lavoro lasciato aperto il venerdì e ripreso il lunedì mattina.

La regola resta la stessa per entrambe le finestre: cambia solo quanto tempo aspettiamo dopo l'ultimo uso.

La finestra decide quante sessioni riprese perdiamo contro quanto spazio teniamo occupato. Il costo dello spazio è basso; il costo di una sessione persa è il lavoro dell'utente.
"""

RETENTION = question("retention", "Quanto teniamo le sessioni chiuse?", RETENTION_CAPIRE, [
    option("day", "1 giorno di inattività", "Spazio minimo; il weekend fa perdere le sessioni.", "Una sessione ripresa il lunedì è già stata eliminata.", True),
    option("week", "7 giorni di inattività", "Copre il weekend; circa sette volte lo spazio.", "La sessione del venerdì è ancora lì il lunedì."),
], "L'inattività riparte a ogni uso.")

RETENTION_REWRITTEN = question("retention", "Quanto teniamo le sessioni chiuse?", RETENTION_CAPIRE, [
    option("day", "1 giorno di inattività", "Spazio minimo; il weekend fa perdere le sessioni.", "Una sessione ripresa il lunedì è già stata eliminata."),
    option("week", "7 giorni di inattività", "Copre il weekend; circa sette volte lo spazio.", "La sessione del venerdì è ancora lì il lunedì.", True),
], "Una settimana copre il weekend senza far crescere troppo il disco.")

CRASH = question("crash", "Cosa succede se una sessione crasha durante il salvataggio?", """
Il salvataggio tronca il file e poi scrive il nuovo contenuto. Se il processo muore tra i due passi, al riavvio il JSON è incompleto e la sessione non si apre più: l'utente vede un errore di parsing e perde tutto lo stato.

Il caso è raro ma non teorico: un terminale chiuso con la sessione in salvataggio, oppure un portatile che si spegne per batteria scarica, bastano a produrlo.

::: evidence
- [observed] `os.WriteFile` apre il file con O_TRUNC e scrive sul posto.
- [observed] Due segnalazioni in un mese di file di stato vuoti dopo un riavvio.
- [unverified] Il rename è atomico su tutti i file system che supportiamo.
:::

| Criterio | File temporaneo + rename | Nessuna protezione |
| --- | --- | --- |
| Stato dopo un crash | vecchio o nuovo, mai a metà | possibile file troncato |
| Codice | poche righe | nessuno |
| Formato | invariato | invariato |

### La sequenza del salvataggio
Oggi il salvataggio esegue due passi: apre il file troncandolo, poi scrive i byte nuovi. Tra i due passi il file esiste ma è vuoto o parziale, e qualunque interruzione lo lascia in quello stato. Con il file temporaneo i passi diventano tre: scrittura completa del temporaneo, fsync, rename sul nome definitivo.

Il rename è atomico solo dentro lo stesso file system, quindi il temporaneo nasce nella stessa cartella del file definitivo e non nella cartella temporanea di sistema, che su alcune macchine è montata altrove.

- Un crash prima del rename lascia intatto il file vecchio e un temporaneo da ignorare.
- Un crash dopo il rename lascia il file nuovo completo.
- Il temporaneo orfano viene rimosso al salvataggio successivo.
- Il costo è un fsync per salvataggio, misurato sotto il millisecondo su SSD.

Il rename sostituisce il file solo quando il nuovo contenuto è completo, quindi un crash lascia sempre una versione leggibile.
""", [
    option("atomic", "File temporaneo + rename", "Il file è sempre vecchio o nuovo; un fsync per salvataggio.", "Il crash avviene sul file temporaneo; quello vero resta integro.", True),
    option("none", "Nessuna protezione", "Zero codice; la sessione interrotta perde lo stato.", "Il file troncato viene scartato e lo stato è perso."),
], "Poche righe e nessun cambio di formato.")

CLEANUP = question("cleanup", "Chi esegue la pulizia delle sessioni scadute?", """
Con la finestra di sette giorni qualcuno deve eliminare le sessioni scadute. Il controllo può partire da solo all'avvio di ogni comando, oppure restare un comando esplicito che l'utente lancia quando vuole liberare spazio.

::: evidence
- [observed] Ogni comando legge già la cartella delle sessioni all'avvio.
- [unverified] Uno sweep su 300 sessioni richiede meno di 10 ms su disco locale.
:::

Lo sweep elimina solo le sessioni scadute che nessun processo tiene aperte: prima prende il lock della sessione, poi ricontrolla la data, e solo allora rimuove la cartella. Una sessione ripresa nel frattempo resta intatta.

Lo sweep all'avvio non chiede niente all'utente; il comando manuale lascia il controllo all'utente ma si dimentica facilmente.
""", [
    option("sweep", "Lo sweep all'avvio", "Automatico; un controllo a ogni comando.", "Le sessioni scadute spariscono al primo comando utile.", True),
    option("manual", "Un comando manuale", "Nessun costo all'avvio; va ricordato.", "Le sessioni restano finché l'utente non lancia il comando."),
], "Nessun comando da ricordare.")

CONFIRM_OPTIONS = [
    option("yes", "Sì, confermo", "Chiudiamo la fase con queste decisioni.", "La fase si chiude.", True),
    option("fix", "No, correggo nella discussione", "Scrivo cosa cambiare nella discussione.", "Riapriamo la domanda indicata."),
]
CONFIRM_EVIDENCE = "::: evidence\n- [unverified] Lo sweep non è misurato su dischi lenti.\n:::"

REPLY_1 = "Il giorno si conta dall'ultimo uso: se riprendi entro 24 ore la sessione resta."
REPLY_2 = "Hai ragione sul weekend: ora consiglio 7 giorni."
WHY = {
    "storage": "Nessuna dipendenza nuova e nessuna attesa tra sessioni.",
    "retention": "Copre il weekend senza far crescere troppo il disco.",
    "crash": "Poche righe e nessun cambio di formato.",
    "cleanup": "Nessun comando da ricordare.",
}

# The simulated user feedback, identical in both formats: choices and, per
# question, messages that the baseline receives as unanchored comments.
BATCHES = [
    ({"storage": "file"}, {"retention": ["1 giorno non è poco?"]}),
    ({"storage": "file"}, {"retention": ["Il weekend però sì: preferisco più margine."]}),
    ({"storage": "file", "retention": "week"}, {}),
    ({"crash": "atomic", "cleanup": "sweep"}, {}),
    ({"confirm": "yes"}, {}),
]


def new_question(q):
    lines = [f'# {q["title"]} {{id="{q["id"]}"}}', "## Capire", q["capire"], "## Decidere"]
    for o in q["options"]:
        lines.append(f'- [{o["id"]}] {o["label"]}' + (" {recommended}" if o["recommended"] else ""))
        lines.append(f'  {o["detail"]}')
        lines.append(f'  => {o["effect"]}')
    lines.append(q["reason"])
    return "\n".join(lines) + "\n"


def new_element(kind, body=""):
    return f"::: {kind}\n{body}\n:::\n" if body else f"::: {kind}\n:::\n"


# The same phase in the per-question call model.
NEW_CALLS = [
    new_element("phase Archivio delle sessioni")
    + "".join(new_question(q) for q in (STORAGE, RETENTION, CRASH))
    + '# Chi esegue la pulizia? {id="cleanup" after="retention"}\n',
    new_element("reply retention", REPLY_1),
    new_question(RETENTION_REWRITTEN) + new_element("reply retention", REPLY_2),
    new_element("settled storage", WHY["storage"]) + new_element("settled retention", WHY["retention"])
    + new_question(CLEANUP).replace('{id="cleanup"}', '{id="cleanup" after="retention"}'),
    new_element("settled crash", WHY["crash"]) + new_element("settled cleanup", WHY["cleanup"])
    + '# Confermi le decisioni? {id="confirm"}\n## Capire\n::: recap\n:::\n' + CONFIRM_EVIDENCE + "\n## Decidere\n"
    + "".join(f'- [{o["id"]}] {o["label"]}' + (" {recommended}" if o["recommended"] else "") + "\n" for o in CONFIRM_OPTIONS),
]


def old_round(questions, notes=None, capire_extra=""):
    """One complete baseline round: every open question's content, then its decisions."""
    notes = notes or {}
    parts = ["# Capire"]
    if capire_extra:
        parts.append(capire_extra)
    for q in questions:
        if "capire" in q:
            parts += [f'## {q["title"]}', q["capire"]]
            if q["id"] in notes:
                parts.append(f'::: info Risposta\n{notes[q["id"]]}\n:::')
    parts.append("# Decidere")
    for q in questions:
        parts.append(f'## {q["title"]} {{id="{q["id"]}"}}')
        if q.get("reason"):
            parts.append(q["reason"])
        for o in q["options"]:
            parts.append(f'- [{o["id"]}] {o["label"]}' + (" (consigliata)" if o["recommended"] else ""))
            parts.append(f'  {o["detail"]} {o["effect"]}')
    return "\n".join(parts) + "\n"


RECAP = "\n".join([
    "| Domanda | Decisione | Round | Perché | Scartate |",
    "| --- | --- | --- | --- | --- |",
    f'| {STORAGE["title"]} | Un file per sessione | r1 | {WHY["storage"]} | Database locale |',
    f'| {RETENTION["title"]} | 7 giorni di inattività | r3 | {WHY["retention"]} | 1 giorno di inattività |',
    f'| {CRASH["title"]} | File temporaneo + rename | r4 | {WHY["crash"]} | Nessuna protezione |',
    f'| {CLEANUP["title"]} | Lo sweep all\'avvio | r4 | {WHY["cleanup"]} | Un comando manuale |',
    CONFIRM_EVIDENCE,
])

# The same phase on the baseline: answers to comments and changed proposals are
# content, so every turn is a complete round; no turn of this walk keeps its
# content unchanged, so none qualifies for --reuse.
OLD_CALLS = [
    old_round([STORAGE, RETENTION, CRASH]),
    old_round([STORAGE, RETENTION, CRASH], {"retention": REPLY_1}),
    old_round([STORAGE, RETENTION_REWRITTEN, CRASH], {"retention": REPLY_2}),
    old_round([CRASH, CLEANUP]),
    old_round([dict(id="confirm", title="Confermi le decisioni?", options=CONFIRM_OPTIONS)], capire_extra=RECAP),
]

SMALL = (STORAGE, {"storage": "file"}, {"storage": ["Va bene, ma teniamo la pulizia esplicita."]})
LARGE_MESSAGES = {"storage": ["Abbiamo osservato contesa in scrittura; usiamo un file per sessione. " * 60],
                  "retention": ["Approvato, con pulizia giornaliera."]}


def count(text):
    text = re.sub(r"http://127\.0\.0\.1:\d+/s/[0-9a-f]{64}/", "http://127.0.0.1:12345/s/" + "a" * 64 + "/", text)
    return {"bytes": len(text.encode()), "tokens": len(ENC.encode(text))}


def command(binary, env, *args):
    result = subprocess.run([str(binary), *args], env=env, capture_output=True, text=True, check=True)
    return "lavagna " + " ".join(args) + "\n" + result.stdout + result.stderr, result.stdout


def batch(baseline, view, submission, choices, messages):
    body = dict(round=view["round"], token=view["token"], submission=submission)
    if baseline:
        body["choices"] = choices
        body["comments"] = [dict(text=text) for texts in messages.values() for text in texts]
    else:
        questions = {qid: {"choice": choice} for qid, choice in choices.items()}
        for qid, texts in messages.items():
            questions.setdefault(qid, {})["messages"] = texts
        body["questions"] = questions
    return body


def present(binary, env, source, body):
    """Run one call, send one batch, and return the agent context of the call."""
    proc = subprocess.Popen([str(binary), "round"], env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        proc.stdin.write(source)
        proc.stdin.close()
        proc.stdin = None
        status = proc.stderr.readline()
        url = re.search(r"http://127\.0\.0\.1:\d+/s/[0-9a-f]{64}/", status)
        if not url:
            raise RuntimeError("No round URL: " + status + proc.stdout.read())
        url = url.group()
        with urllib.request.urlopen(url + "events", timeout=10) as stream:
            for line in stream:
                if line.startswith(b"data: "):
                    view = json.loads(line[6:])
                    break
        request = urllib.request.Request(url + "send", data=json.dumps(body(view)).encode(),
                                         headers={"Origin": url.split("/s/")[0], "Content-Type": "application/json"})
        with urllib.request.urlopen(request, timeout=10) as reply:
            assert reply.status == 202
        stdout, stderr = proc.communicate(timeout=10)
        if proc.returncode:
            raise RuntimeError(stdout + stderr)
        return "lavagna round\n" + source + status + stdout + stderr, json.loads(stdout.splitlines()[-1])
    finally:
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        proc.wait()


def measure(binary, home, baseline):
    def env(session):
        return {"PATH": os.environ["PATH"], "HOME": str(home), "BROWSER": "true", "LAVAGNA_SESSION": session}

    help_context, _ = command(binary, env("phase"), "round", "--help")
    calls, rounds = [], []
    for i, (source, (choices, messages)) in enumerate(zip(OLD_CALLS if baseline else NEW_CALLS, BATCHES), 1):
        submission = f"s-{i:016x}"
        context, result = present(binary, env("phase"), source,
                                  lambda view: batch(baseline, view, submission, choices, messages))
        assert result.get("lavagna") == "feedback", result
        calls.append(count(context))
        rounds.append(result["round"])
    # The baseline opens a round per turn; the call model stays in r1 until a new question arrives.
    assert rounds == (["r1", "r2", "r3", "r4", "r5"] if baseline else ["r1", "r1", "r1", "r2", "r3"]), rounds

    q, choices, messages = SMALL
    first = old_round([q]) if baseline else new_question(q)
    small, _ = present(binary, env("small"), first, lambda view: batch(baseline, view, "s-00000000000000a1", choices, messages))

    source = old_round([STORAGE, RETENTION]) if baseline else new_question(STORAGE) + new_question(RETENTION)
    large, result = present(binary, env("large"), source,
                            lambda view: batch(baseline, view, "s-00000000000000b1", {"storage": "file"}, LARGE_MESSAGES))
    assert result.get("deferred"), result
    submission = result["submission"]
    if baseline:
        overview, _ = command(binary, env("large"), "feedback", submission)
        one, out = command(binary, env("large"), "feedback", submission, "--comment", "2")
        assert json.loads(out)["text"] == LARGE_MESSAGES["retention"][0]
        one = overview + one
    else:
        one, out = command(binary, env("large"), "feedback", submission, "--question", "retention")
        assert json.loads(out)["messages"] == LARGE_MESSAGES["retention"]
    full, out = command(binary, env("large"), "feedback", submission, "--all")
    assert LARGE_MESSAGES["storage"][0] in out

    help_count = count(help_context)
    return {
        "phase_calls": calls,
        "phase_total": {key: help_count[key] + sum(c[key] for c in calls) for key in ("bytes", "tokens")},
        "default_help": help_count,
        "small_feedback_round": count(small),
        "large_feedback_one_question": count(large + one),
        "large_feedback_all": count(large + full),
    }


def main():
    with tempfile.TemporaryDirectory(prefix="lavagna-context-") as directory:
        temp = Path(directory)
        old = temp / "baseline"
        old.mkdir()
        archive = subprocess.check_output(["git", "archive", BASE], cwd=ROOT)
        with tarfile.open(fileobj=io.BytesIO(archive)) as files:
            files.extractall(old, filter="data")
        binaries = [temp / "old", temp / "new"]
        for source, binary in zip([old, ROOT], binaries):
            subprocess.run(["go", "build", "-o", str(binary), "."], cwd=source, check=True)
        results = {}
        for baseline, binary in zip([True, False], binaries):
            home = temp / ("home-old" if baseline else "home-new")
            home.mkdir()
            results["baseline" if baseline else "candidate"] = measure(binary, home, baseline)

    old, new = results["baseline"], results["candidate"]
    checks = {
        "phase": (new["phase_total"]["tokens"], old["phase_total"]["tokens"], PHASE_SHARE),
        "reply_only_call": (new["phase_calls"][1]["tokens"], old["phase_calls"][1]["tokens"], REPLY_SHARE),
        "first_call": (new["phase_calls"][0]["tokens"], old["phase_calls"][0]["tokens"], FIRST_SHARE),
    }
    verdicts = {name: {"candidate": got, "baseline": base, "share": round(got / base, 3), "limit": limit, "pass": got <= limit * base}
                for name, (got, base, limit) in checks.items()}
    print(json.dumps({"baseline": BASE, "tokenizer": "tiktoken 0.14.0 / o200k_base",
                      "normalization": "Only random capability URLs and ports are fixed; both output streams count.",
                      "checks": verdicts, "measurements": results}, indent=2))
    failed = [name for name, v in verdicts.items() if not v["pass"]]
    if failed:
        print("context regression: " + ", ".join(failed), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
